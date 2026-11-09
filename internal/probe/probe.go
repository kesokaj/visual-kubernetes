// Package probe checks, once, which built-in data sources the binary can read on the current
// cluster, and reports each as ok, missing, denied, unreachable or error with a reason. The
// live sources (Phase 2) reuse the same states so the API server panel can show them (PRD R4).
package probe

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"

	"github.com/kesokaj/visual-kubernetes/internal/kubelet"
)

// State is a source's capability state.
type State string

// The five capability states, as printed by `visual-kubernetes probe`.
const (
	OK          State = "ok"
	Missing     State = "missing"
	Denied      State = "denied"
	Unreachable State = "unreachable"
	Error       State = "error"
)

// Result is one source's outcome.
type Result struct {
	Source string
	State  State
	Detail string
}

// RawGetter GETs an absolute API server path (`/metrics`, `/readyz`, an aggregated API).
type RawGetter interface {
	Get(ctx context.Context, path string) ([]byte, error)
}

// SummaryFetcher reads one kubelet's Summary API.
type SummaryFetcher interface {
	Summary(ctx context.Context, host string, port int) (*kubelet.Summary, error)
}

// DialFunc opens a TCP connection; net.Dialer.DialContext in production.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// Deps are the clients the probes use. Kubelet may be nil, with KubeletErr saying why it could
// not be built (for example a config without a CA bundle); that source then reports error.
type Deps struct {
	Client     kubernetes.Interface
	Raw        RawGetter
	Kubelet    SummaryFetcher
	KubeletErr error
	Dial       DialFunc
	// Timeout bounds each probe; zero means 10 s.
	Timeout time.Duration
}

type probeFunc func(ctx context.Context, d Deps) (detail string, err error)

type source struct {
	name string
	run  probeFunc
}

// sources is the fixed probe order the output follows.
var sources = []source{
	{"discovery", probeDiscovery},
	{"leases", probeLeases},
	{"events", probeEvents},
	{"endpointslices", probeEndpointSlices},
	{"metrics.k8s.io", probeMetricsAPI},
	{"kubelet", probeKubelet},
	{"apiserver-metrics", probeAPIServerMetrics},
	{"readyz", probeReadyz},
	{"hubble-relay", probeHubble},
}

// Run executes every probe concurrently, each under its own timeout, and returns the results in
// the fixed source order. It never fails: a probe's failure is its result.
func Run(ctx context.Context, d Deps) []Result {
	timeout := d.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	results := make([]Result, len(sources))
	var wg sync.WaitGroup
	for i, s := range sources {
		wg.Go(func() {
			pctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			detail, err := s.run(pctx, d)
			results[i] = toResult(s.name, detail, err)
		})
	}
	wg.Wait()
	return results
}

func toResult(name, detail string, err error) Result {
	if err == nil {
		return Result{Source: name, State: OK, Detail: oneLine(detail)}
	}
	var se stateError
	if errors.As(err, &se) {
		return Result{Source: name, State: se.state, Detail: oneLine(se.detail)}
	}
	return Result{Source: name, State: Classify(err), Detail: oneLine(err.Error())}
}

// stateError lets a probe report a state directly (for example "missing" when no Hubble Service
// exists, which is not an API error).
type stateError struct {
	state  State
	detail string
}

func (e stateError) Error() string { return string(e.state) + ": " + e.detail }

// Classify maps an error from client-go, the kubelet client or a dial to a capability state.
// TLS verification failures are checked first: a cert error wrapped in a *net.OpError would
// otherwise read as merely unreachable and hide an R8 problem.
func Classify(err error) State {
	if err == nil {
		return OK
	}
	var certErr *tls.CertificateVerificationError
	var unknownCA x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	if errors.As(err, &certErr) || errors.As(err, &unknownCA) || errors.As(err, &hostErr) {
		return Error
	}
	switch {
	case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
		return Denied
	case apierrors.IsNotFound(err):
		return Missing
	case apierrors.IsServiceUnavailable(err), apierrors.IsTimeout(err), apierrors.IsServerTimeout(err):
		return Unreachable
	}
	var statusErr *kubelet.StatusError
	if errors.As(err, &statusErr) {
		switch statusErr.Code {
		case 401, 403:
			return Denied
		case 404:
			return Missing
		}
		return Error
	}
	var opErr *net.OpError
	var dnsErr *net.DNSError
	var urlErr *url.Error
	if errors.As(err, &opErr) || errors.As(err, &dnsErr) ||
		errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &urlErr) && urlErr.Timeout()) {
		return Unreachable
	}
	return Error
}

// Write prints one line per result: `<source> <state> <detail>`.
func Write(w io.Writer, results []Result) error {
	for _, r := range results {
		detail := r.Detail
		if detail == "" {
			detail = "-"
		}
		if _, err := fmt.Fprintf(w, "%s %s %s\n", r.Source, r.State, detail); err != nil {
			return err
		}
	}
	return nil
}

const maxDetail = 240

// ErrLocalMode is the KubeletErr for a run outside the cluster: the kubelet client is never
// built there, so the operator's own (often cluster-admin) credentials never reach a node.
var ErrLocalMode = errors.New("local mode: kubelet not probed; operator credentials are never sent to nodes")

// oneLine collapses whitespace, drops control characters (a hostile kubelet or API body must not
// inject terminal escapes into the operator's output) and caps the length.
func oneLine(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && !unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > maxDetail {
		s = s[:maxDetail] + "…"
	}
	return s
}
