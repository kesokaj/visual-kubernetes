package probe

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// TestRESTRawClassifiesStatus checks the production raw getter turns API server answers into
// typed apierrors, so Classify maps them to the right states.
func TestRESTRawClassifiesStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/readyz":
			_, _ = w.Write([]byte("ok")) // test server
		case "/metrics":
			w.WriteHeader(http.StatusForbidden)
		case "/apis/metrics.k8s.io/v1beta1/nodes":
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	t.Cleanup(srv.Close)
	cs, err := kubernetes.NewForConfig(&rest.Config{Host: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	raw := RESTRaw{Client: cs.Discovery().RESTClient()}
	tests := []struct {
		path string
		want State
	}{
		{"/readyz", OK},
		{"/metrics", Denied},
		{"/apis/metrics.k8s.io/v1beta1/nodes", Missing},
		{"/other", Unreachable},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			_, err := raw.Get(t.Context(), tt.path)
			if got := Classify(err); got != tt.want {
				t.Errorf("Classify(Get(%s)) = %s (%v), want %s", tt.path, got, err, tt.want)
			}
		})
	}
}
