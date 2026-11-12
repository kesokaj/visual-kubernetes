// Package demo implements the opt-in interactive demo endpoints (/api/demo/*) for
// non-production teaching clusters (ADR-010, ADR-011). By default (--demo=false) none of these
// handlers are mounted and /api/demo/* answers 403.
package demo

import (
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

// SourceName is the capability key published on /api/stream for interactive demo mode.
const SourceName = "demo"

// Capability reports the stream capability state for --demo.
func Capability(enabled bool) model.Capability {
	if enabled {
		return model.Capability{State: "ok", Detail: "interactive demo enabled"}
	}
	return model.Capability{State: "disabled", Detail: "interactive demo disabled"}
}

// IsProtectedNamespace reports whether ns is a system or empty namespace that interactive demo
// operations must never proxy, mutate or enter (PRD R1, D3).
func IsProtectedNamespace(ns string) bool {
	s := strings.ToLower(strings.TrimSpace(ns))
	switch s {
	case "", "kube-system", "kube-public", "kube-node-lease", "visual-kubernetes":
		return true
	}
	return strings.HasPrefix(s, "gke-managed-") || strings.HasPrefix(s, "gmp-")
}

// LoopbackOrIAP refuses non-loopback Host headers on every route except GET /healthz whenever
// --demo is enabled without Google IAP (PRD R7, ADR-011, D2).
func LoopbackOrIAP(iapEnabled bool, next http.Handler) http.Handler {
	if iapEnabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		if !isLoopbackHost(r.Host) {
			forbid(w, "interactive demo without --iap-audience requires localhost")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// SameOrigin enforces same-origin Origin and Sec-Fetch-Site headers on mutating endpoints and
// WebSocket handshakes (ADR-011, D2).
func SameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !hasSameOriginHeaders(r) {
			forbid(w, "same-origin request required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func hasSameOriginHeaders(r *http.Request) bool {
	if sfs := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))); sfs != "" && sfs != "same-origin" {
		return false
	}
	origins := r.Header.Values("Origin")
	if len(origins) != 1 || origins[0] == "" || origins[0] == "null" {
		return false
	}
	u, err := url.Parse(origins[0])
	if err != nil || !validOriginURL(u) {
		return false
	}
	defPort := "80"
	if u.Scheme == "https" {
		defPort = "443"
	}
	oh, op, ok1 := splitHostPort(u.Host, defPort)
	rh, rp, ok2 := splitHostPort(strings.TrimSpace(r.Host), defPort)
	return ok1 && ok2 && oh == rh && op == rp
}

func validOriginURL(u *url.URL) bool {
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return u.User == nil && u.Host != "" && u.Path == "" && u.RawQuery == "" && u.Fragment == ""
}

func isLoopbackHost(rawHost string) bool {
	h, _, ok := splitHostPort(strings.TrimSpace(rawHost), "")
	if !ok {
		return false
	}
	if h == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(h)
	return err == nil && (ip == netip.AddrFrom4([4]byte{127, 0, 0, 1}) || ip == netip.IPv6Loopback())
}

func splitHostPort(hostport, defaultPort string) (host, port string, ok bool) {
	if hostport == "" {
		return "", "", false
	}
	if strings.HasPrefix(hostport, "[") {
		if strings.HasSuffix(hostport, "]") {
			h := strings.ToLower(hostport[1 : len(hostport)-1])
			return h, defaultPort, h != ""
		}
		h, p, err := net.SplitHostPort(hostport)
		if err != nil || h == "" || p == "" {
			return "", "", false
		}
		return strings.ToLower(h), p, true
	}
	if strings.Contains(hostport, ":") {
		h, p, err := net.SplitHostPort(hostport)
		if err != nil || h == "" || p == "" {
			return "", "", false
		}
		return strings.ToLower(strings.TrimSuffix(h, ".")), p, true
	}
	h := strings.ToLower(strings.TrimSuffix(hostport, "."))
	return h, defaultPort, h != ""
}

func forbid(w http.ResponseWriter, msg string) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.Error(w, msg, http.StatusForbidden)
}
