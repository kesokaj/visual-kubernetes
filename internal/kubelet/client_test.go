package kubelet

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/client-go/rest"
)

const summaryJSON = `{"node":{"nodeName":"kind-worker","network":{"time":"2026-11-09T18:00:00Z","rxBytes":100,"txBytes":200}},
"pods":[{"podRef":{"name":"web-1","namespace":"demo","uid":"u1"},"network":{"time":"2026-11-09T18:00:00Z","rxBytes":7,"txBytes":9},"unknownField":true}]}`

// kubeletServer is an httptest TLS server standing in for a kubelet; it records whether any
// request reached the handler and which bearer token it carried.
func kubeletServer(t *testing.T, status int) (*httptest.Server, *atomic.Value, *atomic.Int32) {
	t.Helper()
	var token atomic.Value
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		token.Store(r.Header.Get("Authorization"))
		if r.URL.Path != "/stats/summary" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(summaryJSON)) // test server; the client asserts on what arrives
	}))
	t.Cleanup(srv.Close)
	return srv, &token, &hits
}

func caPEM(srv *httptest.Server) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
}

// otherCAPEM returns a freshly generated self-signed CA that signed nothing the server presents.
func otherCAPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "untrusted-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func hostPort(t *testing.T, srv *httptest.Server) (string, int) {
	t.Helper()
	host, p, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(p)
	if err != nil {
		t.Fatal(err)
	}
	return host, port
}

func TestClientFetchesSummaryWithVerifiedTLSAndToken(t *testing.T) {
	srv, token, _ := kubeletServer(t, http.StatusOK)
	c, err := New(&rest.Config{BearerToken: "sa-token", TLSClientConfig: rest.TLSClientConfig{CAData: caPEM(srv)}}, 0, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	host, port := hostPort(t, srv)
	s, err := c.Summary(t.Context(), host, port)
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if got := token.Load(); got != "Bearer sa-token" {
		t.Errorf("Authorization = %q, want the ServiceAccount bearer token", got)
	}
	if s.Node.NodeName != "kind-worker" || *s.Node.Network.RxBytes != 100 {
		t.Errorf("node = %+v", s.Node)
	}
	if len(s.Pods) != 1 || s.Pods[0].PodRef.Name != "web-1" || *s.Pods[0].Network.TxBytes != 9 {
		t.Errorf("pods = %+v", s.Pods)
	}
}

// TestClientRefusesUnverifiedCert proves R8: a kubelet whose cert does not chain to the
// configured CA is refused at the handshake, so the token is never sent to it.
func TestClientRefusesUnverifiedCert(t *testing.T) {
	srv, _, hits := kubeletServer(t, http.StatusOK)
	// Every httptest server shares one built-in cert, so the untrusted CA is generated here.
	c, err := New(&rest.Config{BearerToken: "sa-token", TLSClientConfig: rest.TLSClientConfig{CAData: otherCAPEM(t)}}, 0, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	host, port := hostPort(t, srv)
	_, err = c.Summary(t.Context(), host, port)
	if err == nil {
		t.Fatal("Summary succeeded against a kubelet cert signed by an untrusted CA")
	}
	var verr *tls.CertificateVerificationError
	if !errors.As(err, &verr) {
		t.Errorf("err = %v, want a certificate verification error", err)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("handler reached %d times; the request (and token) must not be sent", n)
	}
}

func TestNewRejectsUnsafeConfigs(t *testing.T) {
	tests := []struct {
		name string
		cfg  *rest.Config
		want error
	}{
		{"insecure", &rest.Config{TLSClientConfig: rest.TLSClientConfig{Insecure: true}}, ErrInsecureConfig},
		{"insecure with CA", &rest.Config{TLSClientConfig: rest.TLSClientConfig{Insecure: true, CAData: []byte("x")}}, ErrInsecureConfig},
		{"no CA", &rest.Config{BearerToken: "t"}, ErrNoCA},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.cfg, 0, time.Second)
			if !errors.Is(err, tt.want) {
				t.Errorf("New err = %v, want %v", err, tt.want)
			}
		})
	}
}

// TestNewIgnoresAPIServerName checks a kubeconfig tls-server-name meant for the API server does
// not leak into kubelet verification (it would make every kubelet fail, or worse, pin one name).
func TestNewIgnoresAPIServerName(t *testing.T) {
	srv, _, _ := kubeletServer(t, http.StatusOK)
	c, err := New(&rest.Config{TLSClientConfig: rest.TLSClientConfig{CAData: caPEM(srv), ServerName: "kubernetes.default.svc"}}, 0, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	host, port := hostPort(t, srv)
	if _, err := c.Summary(t.Context(), host, port); err != nil {
		t.Fatalf("Summary: %v", err)
	}
}

// TestClientDoesNotFollowRedirects: a verified kubelet must not be able to bounce the token to
// another host; the redirect comes back as a StatusError and the target is never contacted.
func TestClientDoesNotFollowRedirects(t *testing.T) {
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetHits.Add(1) }))
	t.Cleanup(target.Close)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/steal", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	c, err := New(&rest.Config{BearerToken: "sa-token", TLSClientConfig: rest.TLSClientConfig{CAData: caPEM(srv)}}, 0, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	host, port := hostPort(t, srv)
	_, err = c.Summary(t.Context(), host, port)
	var serr *StatusError
	if !errors.As(err, &serr) || serr.Code != http.StatusFound {
		t.Errorf("err = %v, want StatusError 302", err)
	}
	if n := targetHits.Load(); n != 0 {
		t.Errorf("redirect target contacted %d times", n)
	}
}

func TestSummaryStatusError(t *testing.T) {
	srv, _, _ := kubeletServer(t, http.StatusForbidden)
	c, err := New(&rest.Config{TLSClientConfig: rest.TLSClientConfig{CAData: caPEM(srv)}}, 0, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	host, port := hostPort(t, srv)
	_, err = c.Summary(t.Context(), host, port)
	var serr *StatusError
	if !errors.As(err, &serr) || serr.Code != http.StatusForbidden {
		t.Fatalf("err = %v, want StatusError 403", err)
	}
	if !strings.Contains(serr.Error(), "403") {
		t.Errorf("Error() = %q", serr.Error())
	}
}
