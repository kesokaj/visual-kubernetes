package iapauth

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/api/idtoken"
)

const testAudience = "/projects/123456789012/global/backendServices/9876543210"

type stubToken struct {
	audience string
	payload  *idtoken.Payload
	err      error
}

type stubValidator struct {
	tokens map[string]stubToken
}

func (s stubValidator) Validate(_ context.Context, rawToken, audience string) (*idtoken.Payload, error) {
	tok, ok := s.tokens[rawToken]
	if !ok {
		return nil, errors.New("invalid signature")
	}
	if tok.err != nil {
		return nil, tok.err
	}
	if tok.audience != audience {
		return nil, errors.New("audience mismatch")
	}
	return tok.payload, nil
}

func TestValidateAudience(t *testing.T) {
	valid := []string{
		"/projects/123456789012/global/backendServices/9876543210",
		"/projects/123456789012/locations/europe-north1/services/visual-kubernetes",
		"/projects/000000000000/global/backendServices/0000000000000000",
	}
	for _, aud := range valid {
		if err := ValidateAudience(aud); err != nil {
			t.Errorf("ValidateAudience(%q) unexpected error: %v", aud, err)
		}
	}

	invalid := []string{
		"",
		"   ",
		"projects/12345/global/backendServices/1",
		"/projects/",
		"/projects/not-a-number/global/backendServices/1",
		"/projects/12345/",
		"/projects/12345",
		"https://vk.example.com",
		"/projects/12345/global/backendServices/1 extra",
	}
	for _, aud := range invalid {
		if err := ValidateAudience(aud); err == nil {
			t.Errorf("ValidateAudience(%q) succeeded, want error", aud)
		}
	}
}

func TestMiddlewareRejectsBadStartupConfig(t *testing.T) {
	if _, err := NewMiddleware(stubValidator{}, "", nil); err == nil {
		t.Fatal("empty audience accepted")
	}
	if _, err := NewMiddleware(stubValidator{}, "https://example.com", nil); err == nil {
		t.Fatal("non-/projects/ audience accepted")
	}
	if _, err := NewMiddleware(nil, testAudience, nil); err == nil {
		t.Fatal("nil validator accepted")
	}
}

func TestMiddlewareEnforcesSignedIAPAssertion(t *testing.T) {
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))

	validator := stubValidator{
		tokens: map[string]stubToken{
			"valid-jwt-secret-value": {
				audience: testAudience,
				payload: &idtoken.Payload{
					Issuer:   Issuer,
					Audience: testAudience,
					Subject:  "accounts.google.com:112233",
					Claims:   map[string]any{"email": "presenter@example.com"},
				},
			},
			"expired-jwt-secret-value": {
				audience: testAudience,
				err:      errors.New("token expired"),
			},
			"wrong-aud-jwt-secret-value": {
				audience: "/projects/999999999999/global/backendServices/111111",
				payload: &idtoken.Payload{
					Issuer:   Issuer,
					Audience: "/projects/999999999999/global/backendServices/111111",
					Subject:  "accounts.google.com:112233",
				},
			},
			"wrong-iss-jwt-secret-value": {
				audience: testAudience,
				payload: &idtoken.Payload{
					Issuer:   "https://accounts.google.com",
					Audience: testAudience,
					Subject:  "accounts.google.com:112233",
				},
			},
		},
	}

	mw, err := NewMiddleware(validator, testAudience, logger)
	if err != nil {
		t.Fatalf("NewMiddleware: %v", err)
	}

	called := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	cases := []struct {
		name       string
		method     string
		path       string
		host       string
		assertion  string
		spoofEmail string
		wantStatus int
		wantCalled bool
	}{
		{
			name:       "missing assertion on UI root",
			method:     http.MethodGet,
			path:       "/",
			host:       "vk.demo.example.com",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "spoofed X-Goog-Authenticated-User-Email without signed JWT",
			method:     http.MethodGet,
			path:       "/api/stream",
			host:       "vk.demo.example.com",
			spoofEmail: "accounts.google.com:admin@example.com",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "expired JWT",
			method:     http.MethodGet,
			path:       "/api/objects?id=core/Pod/shop/web-0",
			host:       "vk.demo.example.com",
			assertion:  "expired-jwt-secret-value",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "wrong audience JWT",
			method:     http.MethodGet,
			path:       "/api/demo/ports",
			host:       "vk.demo.example.com",
			assertion:  "wrong-aud-jwt-secret-value",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "wrong issuer JWT",
			method:     http.MethodGet,
			path:       "/api/demo/ports",
			host:       "vk.demo.example.com",
			assertion:  "wrong-iss-jwt-secret-value",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "pod IP GET /healthz exempted for kubelet/GLB probe",
			method:     http.MethodGet,
			path:       "/healthz",
			host:       "10.24.1.15:8080",
			wantStatus: http.StatusOK,
			wantCalled: true,
		},
		{
			name:       "IPv6 pod IP GET /healthz exempted",
			method:     http.MethodGet,
			path:       "/healthz",
			host:       "[fd00::15]:8080",
			wantStatus: http.StatusOK,
			wantCalled: true,
		},
		{
			name:       "DNS Host GET /healthz requires IAP JWT",
			method:     http.MethodGet,
			path:       "/healthz",
			host:       "vk.demo.example.com",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "pod IP non-healthz route still requires IAP JWT",
			method:     http.MethodGet,
			path:       "/api/stream",
			host:       "10.24.1.15:8080",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "valid signed IAP JWT succeeds",
			method:     http.MethodGet,
			path:       "/api/demo/ports",
			host:       "vk.demo.example.com",
			assertion:  "valid-jwt-secret-value",
			wantStatus: http.StatusOK,
			wantCalled: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called = false
			req := httptest.NewRequest(tc.method, "https://"+tc.host+tc.path, nil)
			req.Host = tc.host
			if tc.assertion != "" {
				req.Header.Set(AssertionHeader, tc.assertion)
			}
			if tc.spoofEmail != "" {
				req.Header.Set("X-Goog-Authenticated-User-Email", tc.spoofEmail)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body=%q)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if called != tc.wantCalled {
				t.Fatalf("downstream called = %v, want %v", called, tc.wantCalled)
			}
		})
	}

	// Raw JWT assertions must never appear in logs.
	logs := logBuf.String()
	for _, secret := range []string{
		"valid-jwt-secret-value",
		"expired-jwt-secret-value",
		"wrong-aud-jwt-secret-value",
		"wrong-iss-jwt-secret-value",
	} {
		if strings.Contains(logs, secret) {
			t.Fatalf("raw JWT leaked into logs: %s", logs)
		}
	}
}
