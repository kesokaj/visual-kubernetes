package stream

import (
	"net"
	"net/http"
	"strings"
)

// HostGuard refuses requests whose Host header names anything but an IP literal, `localhost`
// or one of extra (the --addr host when it is a name). The server has no auth (R7): it relies
// on loopback and port-forward, and a page in the presenter's browser could otherwise rebind
// its own DNS name to 127.0.0.1 and read the stream, objects and logs as same-origin (DNS
// rebinding). An attacker controls only names, never which IP literal the browser sends, so IP
// hosts stay allowed; that also keeps the kubelet's readiness probe (Host = pod IP) working.
func HostGuard(next http.Handler, extra ...string) http.Handler {
	allowed := map[string]bool{"localhost": true}
	for _, h := range extra {
		if h = hostname(h); h != "" {
			allowed[h] = true
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := hostname(r.Host)
		if net.ParseIP(h) == nil && !allowed[h] {
			plain(w, http.StatusForbidden, "host not allowed\n")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hostname strips the port and IPv6 brackets and lowercases, with a trailing dot removed so
// `localhost.` is not a way around the list.
func hostname(hostport string) string {
	h := hostport
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		h = host
	}
	h = strings.TrimSuffix(strings.Trim(h, "[]"), ".")
	return strings.ToLower(h)
}
