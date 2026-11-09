// Package kubelet reads the kubelet Summary API (`/stats/summary` on :10250) directly, with the
// kubelet's serving certificate always verified against a cluster CA (PRD R8). It never goes
// through the API server's nodes/proxy, which would need exec-equivalent RBAC.
package kubelet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"k8s.io/client-go/rest"
)

// DefaultPort is the kubelet's authenticated HTTPS port.
const DefaultPort = 10250

// maxSummaryBytes bounds the response read; a busy node's summary is well under this.
const maxSummaryBytes = 16 << 20

// ErrInsecureConfig is returned when the REST config would skip TLS verification.
var ErrInsecureConfig = errors.New("kubelet: refusing a config that skips TLS verification")

// ErrNoCA is returned when the REST config names no CA bundle to verify kubelets against.
var ErrNoCA = errors.New("kubelet: no CA bundle to verify kubelet serving certificates")

// StatusError is a non-200 answer from a kubelet.
type StatusError struct {
	Code int
	Body string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("kubelet: HTTP %d: %s", e.Code, e.Body)
}

// Client fetches Summary stats from kubelets.
type Client struct {
	http *http.Client
	port int
}

// New builds a Client from the same REST config the API client uses: its CA bundle verifies the
// kubelet serving certs and its credentials (in-cluster: the ServiceAccount token file, re-read
// on rotation by client-go) authenticate the request. The config is copied and its ServerName
// cleared so each kubelet is verified against its own address, never the API server's name.
func New(cfg *rest.Config, port int, timeout time.Duration) (*Client, error) {
	if cfg == nil {
		return nil, errors.New("kubelet: nil REST config")
	}
	if cfg.Insecure {
		return nil, ErrInsecureConfig
	}
	if len(cfg.CAData) == 0 && cfg.CAFile == "" {
		return nil, ErrNoCA
	}
	c := rest.CopyConfig(cfg)
	c.ServerName = ""
	rt, err := rest.TransportFor(c)
	if err != nil {
		return nil, fmt.Errorf("kubelet: building transport: %w", err)
	}
	if port == 0 {
		port = DefaultPort
	}
	// Never follow redirects: client-go's bearer round-tripper would re-attach the token to
	// whatever host (or plain-http URL) a kubelet redirects to.
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{http: &http.Client{Transport: rt, Timeout: timeout, CheckRedirect: noRedirect}, port: port}, nil
}

// Summary fetches `/stats/summary?only_cpu_and_memory=false` from the kubelet at host (an IP or
// DNS name from the Node's status). port 0 uses the client's default.
func (c *Client) Summary(ctx context.Context, host string, port int) (*Summary, error) {
	if port == 0 {
		port = c.port
	}
	url := "https://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/stats/summary"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("kubelet: building request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kubelet: GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }() // read-only body; a close error changes nothing
	body := io.LimitReader(resp.Body, maxSummaryBytes)
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(body, 256)) // best-effort detail for the error
		return nil, &StatusError{Code: resp.StatusCode, Body: string(snippet)}
	}
	var s Summary
	if err := json.NewDecoder(body).Decode(&s); err != nil {
		return nil, fmt.Errorf("kubelet: decoding summary from %s: %w", host, err)
	}
	return &s, nil
}
