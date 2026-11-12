// Package iapauth verifies Google Identity-Aware Proxy (IAP) signed JWT assertions on incoming
// HTTP requests using google.golang.org/api/idtoken (ADR-011, PRD R7). It never parses JWTs by
// hand, never trusts plain email headers such as X-Goog-Authenticated-User-Email, and never logs
// raw assertion tokens.
package iapauth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"google.golang.org/api/idtoken"
)

const (
	// Issuer is the sole JWT issuer accepted for Google Identity-Aware Proxy assertions.
	// idtoken.Validate verifies signature, expiry, and audience, so this package checks Issuer too.
	Issuer = "https://cloud.google.com/iap"

	// AssertionHeader is the HTTP header carrying IAP's signed JWT assertion.
	AssertionHeader = "x-goog-iap-jwt-assertion"
)

// Validator validates an ID token string against an expected audience.
// *idtoken.Validator satisfies this interface directly.
type Validator interface {
	Validate(ctx context.Context, idToken, audience string) (*idtoken.Payload, error)
}

// NewDefaultValidator constructs Google's standard idtoken Validator.
func NewDefaultValidator(ctx context.Context) (Validator, error) {
	v, err := idtoken.NewValidator(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating idtoken validator: %w", err)
	}
	return v, nil
}

// ValidateAudience checks that audience is a non-empty IAP resource path starting with
// "/projects/<project-number>/..." so idtoken.Validate never skips audience verification.
func ValidateAudience(audience string) error {
	aud := strings.TrimSpace(audience)
	if aud == "" {
		return errors.New("iapauth: --iap-audience must not be empty")
	}
	if aud != audience || strings.ContainsAny(aud, " \t\r\n") {
		return errors.New("iapauth: --iap-audience must not contain whitespace")
	}
	if !strings.HasPrefix(aud, "/projects/") {
		return errors.New(`iapauth: --iap-audience must start with "/projects/"`)
	}
	rest := strings.TrimPrefix(aud, "/projects/")
	projNum, tail, ok := strings.Cut(rest, "/")
	if !ok || projNum == "" || tail == "" || !allDigits(projNum) {
		return errors.New(`iapauth: --iap-audience must have shape "/projects/<PROJECT_NUMBER>/<resource>"`)
	}
	return nil
}

func allDigits(s string) bool {
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}

// NewMiddleware returns an HTTP middleware that requires a valid signed IAP assertion matching
// audience and Issuer on every route, exempting only GET /healthz when Host is an IP literal
// (kubelet and Google Cloud Load Balancer pod-IP health probes).
func NewMiddleware(validator Validator, audience string, log *slog.Logger) (func(http.Handler) http.Handler, error) {
	if err := ValidateAudience(audience); err != nil {
		return nil, err
	}
	if validator == nil {
		return nil, errors.New("iapauth: validator must not be nil")
	}
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isPodIPHealthProbe(r) {
				next.ServeHTTP(w, r)
				return
			}
			assertion := strings.TrimSpace(r.Header.Get(AssertionHeader))
			if assertion == "" {
				unauthorized(w)
				return
			}
			payload, err := validator.Validate(r.Context(), assertion, audience)
			if err != nil {
				log.WarnContext(r.Context(), "iap assertion rejected", "path", r.URL.Path, "reason", "validation_failed")
				unauthorized(w)
				return
			}
			if payload == nil || payload.Issuer != Issuer {
				log.WarnContext(r.Context(), "iap assertion rejected", "path", r.URL.Path, "reason", "issuer_mismatch")
				unauthorized(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

func isPodIPHealthProbe(r *http.Request) bool {
	if r.Method != http.MethodGet || r.URL.Path != "/healthz" {
		return false
	}
	h := hostname(r.Host)
	return h != "" && net.ParseIP(h) != nil
}

func hostname(hostport string) string {
	h := strings.TrimSpace(hostport)
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	h = strings.TrimSuffix(strings.Trim(h, "[]"), ".")
	return strings.ToLower(h)
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}
