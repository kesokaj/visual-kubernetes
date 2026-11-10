package stream

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHostGuard: loopback names, IP literals (port-forward, the kubelet probe) and the --addr
// name pass; any other name is refused before the handler runs, which is what stops a DNS
// rebinding page from reading the stream.
func TestHostGuard(t *testing.T) {
	reached := false
	h := HostGuard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}), "demo.lan:8080", "")
	for _, tc := range []struct {
		host string
		want int
	}{
		{"localhost:8080", http.StatusOK},
		{"LOCALHOST", http.StatusOK},
		{"127.0.0.1:8080", http.StatusOK},
		{"[::1]:8080", http.StatusOK},
		{"10.244.1.7:8080", http.StatusOK}, // kubelet readiness probe
		{"demo.lan:8080", http.StatusOK},   // the --addr name
		{"evil.example", http.StatusForbidden},
		{"evil.example:8080", http.StatusForbidden},
		{"localhost.evil.example", http.StatusForbidden},
		{"", http.StatusForbidden},
	} {
		reached = false
		req := httptest.NewRequest(http.MethodGet, "/api/stream", nil)
		req.Host = tc.host
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("Host %q: status %d, want %d", tc.host, rec.Code, tc.want)
		}
		if reached != (tc.want == http.StatusOK) {
			t.Errorf("Host %q: handler reached = %v", tc.host, reached)
		}
	}
}
