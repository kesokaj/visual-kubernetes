package demo

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCapability(t *testing.T) {
	on := Capability(true)
	if on.State != "ok" || on.Detail != "interactive demo enabled" {
		t.Errorf("Capability(true) = %+v, want ok / interactive demo enabled", on)
	}
	off := Capability(false)
	if off.State != "disabled" || off.Detail == "" {
		t.Errorf("Capability(false) = %+v, want disabled with detail", off)
	}
}

func TestIsProtectedNamespace(t *testing.T) {
	tests := []struct {
		ns   string
		want bool
	}{
		{"", true},
		{"   ", true},
		{"kube-system", true},
		{"KUBE-SYSTEM", true},
		{"kube-public", true},
		{"kube-node-lease", true},
		{"visual-kubernetes", true},
		{"gke-managed-dpv2-observability", true},
		{"gke-managed-system", true},
		{"gmp-system", true},
		{"gmp-public", true},
		{"default", false},
		{"shop", false},
		{"vk-demo", false},
		{"kube-apps", false},
	}
	for _, tc := range tests {
		if got := IsProtectedNamespace(tc.ns); got != tc.want {
			t.Errorf("IsProtectedNamespace(%q) = %v, want %v", tc.ns, got, tc.want)
		}
	}
}

func TestLoopbackOrIAP(t *testing.T) {
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	guarded := LoopbackOrIAP(false, okHandler)
	iapOn := LoopbackOrIAP(true, okHandler)

	tests := []struct {
		name     string
		handler  http.Handler
		method   string
		path     string
		host     string
		wantCode int
	}{
		{"localhost:8080 allowed", guarded, http.MethodGet, "/", "localhost:8080", http.StatusOK},
		{"localhost trailing dot allowed", guarded, http.MethodGet, "/api/demo/ports", "localhost.:8080", http.StatusOK},
		{"127.0.0.1:8080 allowed", guarded, http.MethodGet, "/api/stream", "127.0.0.1:8080", http.StatusOK},
		{"[::1]:8080 allowed", guarded, http.MethodGet, "/", "[::1]:8080", http.StatusOK},
		{"[0:0:0:0:0:0:0:1]:8080 expanded ipv6 allowed", guarded, http.MethodGet, "/", "[0:0:0:0:0:0:0:1]:8080", http.StatusOK},
		{"::1 bare allowed", guarded, http.MethodGet, "/", "[::1]", http.StatusOK},
		{"pod IP GET /healthz exempt", guarded, http.MethodGet, "/healthz", "10.244.0.5:8080", http.StatusOK},
		{"pod IP POST /healthz refused", guarded, http.MethodPost, "/healthz", "10.244.0.5:8080", http.StatusForbidden},
		{"pod IP GET / refused", guarded, http.MethodGet, "/", "10.244.0.5:8080", http.StatusForbidden},
		{"LB IP GET /api/demo/ports refused", guarded, http.MethodGet, "/api/demo/ports", "203.0.113.10:80", http.StatusForbidden},
		{"127.0.0.2 refused", guarded, http.MethodGet, "/", "127.0.0.2:8080", http.StatusForbidden},
		{"DNS host without IAP refused", guarded, http.MethodGet, "/", "demo.example.com", http.StatusForbidden},
		{"empty host refused", guarded, http.MethodGet, "/", "", http.StatusForbidden},
		{"DNS host with IAP allowed", iapOn, http.MethodGet, "/", "demo.example.com", http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Host = tc.host
			rec := httptest.NewRecorder()
			tc.handler.ServeHTTP(rec, req)
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.wantCode == http.StatusForbidden {
				if !strings.Contains(rec.Body.String(), "interactive demo without --iap-audience requires localhost") {
					t.Errorf("403 body = %q, want localhost notice", rec.Body.String())
				}
				if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
					t.Error("missing nosniff header")
				}
			}
		})
	}
}

func TestSameOrigin(t *testing.T) {
	guarded := SameOrigin(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		name     string
		host     string
		origins  []string
		sfs      string
		wantCode int
	}{
		{"matching http origin + same-origin", "127.0.0.1:8080", []string{"http://127.0.0.1:8080"}, "same-origin", http.StatusOK},
		{"matching localhost origin without sfs", "localhost:8080", []string{"http://localhost:8080"}, "", http.StatusOK},
		{"matching ipv6 origin", "[::1]:8080", []string{"http://[::1]:8080"}, "same-origin", http.StatusOK},
		{"https default port 443 match", "demo.example.com", []string{"https://demo.example.com:443"}, "same-origin", http.StatusOK},
		{"missing Origin refused", "127.0.0.1:8080", nil, "same-origin", http.StatusForbidden},
		{"null Origin (sandboxed iframe) refused", "127.0.0.1:8080", []string{"null"}, "cross-site", http.StatusForbidden},
		{"null Origin even with empty sfs refused", "127.0.0.1:8080", []string{"null"}, "", http.StatusForbidden},
		{"multiple Origin headers refused", "127.0.0.1:8080", []string{"http://127.0.0.1:8080", "http://evil.example"}, "same-origin", http.StatusForbidden},
		{"different port refused", "127.0.0.1:8080", []string{"http://127.0.0.1:3000"}, "same-site", http.StatusForbidden},
		{"different host refused", "127.0.0.1:8080", []string{"http://evil.example"}, "cross-site", http.StatusForbidden},
		{"cross-site Sec-Fetch-Site refused", "127.0.0.1:8080", []string{"http://127.0.0.1:8080"}, "cross-site", http.StatusForbidden},
		{"same-site Sec-Fetch-Site refused", "127.0.0.1:8080", []string{"http://127.0.0.1:8080"}, "same-site", http.StatusForbidden},
		{"non-http scheme refused", "127.0.0.1:8080", []string{"chrome-extension://127.0.0.1:8080"}, "", http.StatusForbidden},
		{"origin with path refused", "127.0.0.1:8080", []string{"http://127.0.0.1:8080/extra"}, "same-origin", http.StatusForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/demo/ports", nil)
			req.Host = tc.host
			for _, o := range tc.origins {
				req.Header.Add("Origin", o)
			}
			if tc.sfs != "" {
				req.Header.Set("Sec-Fetch-Site", tc.sfs)
			}
			rec := httptest.NewRecorder()
			guarded.ServeHTTP(rec, req)
			if rec.Code != tc.wantCode {
				t.Errorf("status = %d, want %d (body %q)", rec.Code, tc.wantCode, rec.Body.String())
			}
		})
	}
}
