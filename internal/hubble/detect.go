// Package hubble streams network flows from Cilium or GKE Dataplane V2 Hubble Relay when
// present (PRD S3, never enabling it), falling back to read-only Prometheus edge queries when
// Relay is absent, and publishes per-edge `flow` SSE events.
package hubble

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Wire constants for the `flow` SSE event (plan Interfaces § SSE events).
const (
	EventFlow        = "flow"
	VerdictForwarded = "FORWARDED"
	VerdictDropped   = "DROPPED"
	SourceName       = "hubble-relay"
)

const (
	ciliumNamespace = "kube-system"
	dpv2Namespace   = "gke-managed-dpv2-observability"
	relayService    = "hubble-relay"
	dpv2CertSecret  = "hubble-relay-client-certs"
	// GKE Dataplane V2 mints Relay's serving certificate for "relay.<ns>.svc.cluster.local"
	// even though the Service is named "hubble-relay".
	dpv2ServerName = "relay.gke-managed-dpv2-observability.svc.cluster.local"
)

// Flow is the JSON payload of an `event: flow` SSE message. Hubble's flow.Flow carries flow
// counts rather than packet byte lengths (bytes come from kubelet stats, PRD S2), so Bytes is 0
// on Hubble events and Count carries the window's aggregated flows (D53).
type Flow struct {
	Src     string `json:"src"`
	Dst     string `json:"dst"`
	Via     string `json:"via,omitempty"` // drawn Service the traffic was addressed to (D9)
	Verdict string `json:"verdict"`
	Bytes   int64  `json:"bytes"`
	Count   int64  `json:"count,omitempty"`
}

// ErrMissing reports that neither known Hubble Relay Service exists on the cluster.
var ErrMissing = errors.New("no Service kube-system/hubble-relay or gke-managed-dpv2-observability/hubble-relay")

// Target describes a discovered Hubble Relay endpoint and its transport security.
type Target struct {
	Addr string
	TLS  *tls.Config // nil for plaintext kube-system/hubble-relay
}

// Detect checks the Cilium and GKE Dataplane V2 Hubble Relay Services in order. For GKE DPv2
// it loads the mTLS client certificate and CA bundle from the named Secret (PRD R2).
func Detect(ctx context.Context, cs kubernetes.Interface) (Target, error) {
	if t, ok, err := detectCilium(ctx, cs); err != nil || ok {
		return t, err
	}
	return detectDPv2(ctx, cs)
}

func detectCilium(ctx context.Context, cs kubernetes.Interface) (Target, bool, error) {
	svc, err := cs.CoreV1().Services(ciliumNamespace).Get(ctx, relayService, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return Target{}, false, nil
	}
	if err != nil {
		return Target{}, false, err
	}
	addr, err := serviceAddr(svc, 80)
	if err != nil {
		return Target{}, false, err
	}
	return Target{Addr: addr}, true, nil
}

func detectDPv2(ctx context.Context, cs kubernetes.Interface) (Target, error) {
	svc, err := cs.CoreV1().Services(dpv2Namespace).Get(ctx, relayService, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return Target{}, ErrMissing
	}
	if err != nil {
		return Target{}, err
	}
	addr, err := serviceAddr(svc, 443)
	if err != nil {
		return Target{}, err
	}
	sec, err := cs.CoreV1().Secrets(dpv2Namespace).Get(ctx, dpv2CertSecret, metav1.GetOptions{})
	if err != nil {
		return Target{}, fmt.Errorf("reading %s/%s: %w", dpv2Namespace, dpv2CertSecret, err)
	}
	tlsCfg, err := clientTLSConfig(sec.Data)
	if err != nil {
		return Target{}, err
	}
	return Target{Addr: addr, TLS: tlsCfg}, nil
}

func serviceAddr(svc *corev1.Service, defaultPort int) (string, error) {
	if len(svc.Spec.Ports) == 0 {
		return "", fmt.Errorf("%s/%s has no ports", svc.Namespace, svc.Name)
	}
	port := int(svc.Spec.Ports[0].Port)
	if port <= 0 {
		port = defaultPort
	}
	host := fmt.Sprintf("%s.%s.svc", svc.Name, svc.Namespace)
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

func clientTLSConfig(data map[string][]byte) (*tls.Config, error) {
	caPEM, certPEM, keyPEM := data["ca.crt"], data["tls.crt"], data["tls.key"]
	if len(caPEM) == 0 || len(certPEM) == 0 || len(keyPEM) == 0 {
		return nil, fmt.Errorf("%s missing ca.crt, tls.crt or tls.key", dpv2CertSecret)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("%s ca.crt contains no certificates", dpv2CertSecret)
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("parsing %s key pair: %w", dpv2CertSecret, err)
	}
	return &tls.Config{
		RootCAs:      pool,
		Certificates: []tls.Certificate{cert},
		ServerName:   dpv2ServerName,
		MinVersion:   tls.VersionTLS12,
	}, nil
}
