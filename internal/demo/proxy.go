package demo

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// ProxyPrefix is the URL path prefix under which revocable capability tokens serve proxied pages.
	ProxyPrefix = "/api/demo/proxy/t/"
	// ProxyCSP forces every proxied HTML response into an opaque-origin sandbox with no external fetches.
	ProxyCSP = "default-src 'self' 'unsafe-inline' data:; frame-ancestors 'self'; sandbox allow-scripts allow-forms"

	maxPortsPostBytes = 4 << 10
	maxProxyPostBytes = 1 << 20
)

var awsMetadataIPv6 = netip.MustParseAddr("fd00:ec2::254")

// SafeDialer blocks loopback, link-local/metadata, multicast, unspecified, non-IP hosts, port 10250,
// and cluster API server IPs before dialing an upstream Service.
type SafeDialer struct {
	DialAddr     func(ctx context.Context, network, addr string) (net.Conn, error)
	ForbiddenIPs func() []netip.Addr
}

// DialContext validates addr against SSRF rules and dials the literal IP:port.
func (d *SafeDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, fmt.Errorf("unsupported network %q", network)
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid target address: %w", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || isBlockedTargetPort(port) {
		return nil, fmt.Errorf("refused target port %q", portStr)
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return nil, errors.New("refused non-IP dial target")
	}
	ip = ip.Unmap()
	if isForbiddenIP(ip, d.forbiddenList()) {
		return nil, fmt.Errorf("refused dial target IP %s", ip)
	}
	dial := d.DialAddr
	if dial == nil {
		dial = (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	}
	return dial(ctx, network, net.JoinHostPort(ip.String(), portStr))
}

func (d *SafeDialer) forbiddenList() []netip.Addr {
	if d == nil || d.ForbiddenIPs == nil {
		return nil
	}
	return d.ForbiddenIPs()
}

func isForbiddenIP(ip netip.Addr, extra []netip.Addr) bool {
	if !ip.IsValid() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || ip == awsMetadataIPv6 {
		return true
	}
	for _, f := range extra {
		if f.IsValid() && f.Unmap() == ip {
			return true
		}
	}
	return false
}

// Options configures a demo Server.
type Options struct {
	Catalog         Catalog
	Workloads       WorkloadStore
	Services        ServiceStore
	Autoscalers     AutoscalerStore
	Exec            PodExecutor
	MaxExecSessions int
	ExecIdleTimeout time.Duration
	ExecMaxLifetime time.Duration
	Dialer          *SafeDialer
	Transport       http.RoundTripper
	IAPEnabled      bool
	Log             *slog.Logger
}

// Server serves /api/demo/* when --demo is enabled.
type Server struct {
	catalog         Catalog
	workloads       WorkloadStore
	services        ServiceStore
	autoscalers     AutoscalerStore
	exec            PodExecutor
	execGate        *execGate
	execIdleTimeout time.Duration
	execMaxLifetime time.Duration
	gate            *gatekeeper
	transport       http.RoundTripper
	iapEnabled      bool
	log             *slog.Logger
}

// NewServer constructs an interactive demo server with all ports initially closed.
func NewServer(o Options) *Server {
	idle := o.ExecIdleTimeout
	if idle <= 0 {
		idle = defaultExecIdleTimeout
	}
	life := o.ExecMaxLifetime
	if life <= 0 {
		life = defaultExecMaxLifetime
	}
	s := &Server{
		catalog:         o.Catalog,
		workloads:       o.Workloads,
		services:        o.Services,
		autoscalers:     o.Autoscalers,
		exec:            o.Exec,
		execGate:        newExecGate(o.MaxExecSessions),
		execIdleTimeout: idle,
		execMaxLifetime: life,
		gate:            newGatekeeper(),
		transport:       o.Transport,
		iapEnabled:      o.IAPEnabled,
		log:             o.Log,
	}
	if s.log == nil {
		s.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if s.transport == nil {
		dialer := o.Dialer
		if dialer == nil {
			dialer = &SafeDialer{}
		}
		if dialer.ForbiddenIPs == nil && s.catalog != nil {
			dialer.ForbiddenIPs = s.catalog.ForbiddenIPs
		}
		s.transport = &http.Transport{
			DialContext:           dialer.DialContext,
			ResponseHeaderTimeout: 10 * time.Second,
			IdleConnTimeout:       30 * time.Second,
			MaxIdleConnsPerHost:   4,
		}
	}
	return s
}

// Handler returns the guarded /api/demo/* HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/demo/ports", s.handleGetPorts)
	mux.Handle("POST /api/demo/ports", SameOrigin(http.HandlerFunc(s.handlePostPorts)))
	mux.HandleFunc("GET /api/demo/workloads/env", s.handleGetWorkloadEnv)
	mux.Handle("PATCH /api/demo/workloads/env", SameOrigin(http.HandlerFunc(s.handlePatchWorkloadEnv)))
	mux.HandleFunc("GET /api/demo/workloads/scale", s.handleGetWorkloadScale)
	mux.Handle("PATCH /api/demo/workloads/scale", SameOrigin(http.HandlerFunc(s.handlePatchWorkloadScale)))
	mux.HandleFunc("GET /api/demo/services/type", s.handleGetServiceType)
	mux.Handle("PATCH /api/demo/services/type", SameOrigin(http.HandlerFunc(s.handlePatchServiceType)))
	mux.HandleFunc("GET /api/demo/autoscalers", s.handleGetAutoscaler)
	mux.Handle("PATCH /api/demo/autoscalers", SameOrigin(http.HandlerFunc(s.handlePatchAutoscaler)))
	mux.HandleFunc("GET /api/demo/exec", s.handleExec)
	mux.HandleFunc(ProxyPrefix, s.handleProxy)
	mux.HandleFunc("/api/demo/", func(w http.ResponseWriter, _ *http.Request) {
		writePlain(w, http.StatusNotFound, "not found")
	})
	return LoopbackOrIAP(s.iapEnabled, mux)
}

func (s *Server) handleGetPorts(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, PortsResponse{Ports: s.gate.list(s.catalog)})
}

type portToggleRequest struct {
	Namespace string `json:"namespace"`
	Service   string `json:"service"`
	Port      int32  `json:"port"`
	Open      *bool  `json:"open"`
}

func (s *Server) handlePostPorts(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxPortsPostBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var req portToggleRequest
	if err := dec.Decode(&req); err != nil || req.Namespace == "" || req.Service == "" || req.Port < 1 || req.Port > 65535 || req.Open == nil {
		writePlain(w, http.StatusBadRequest, "invalid port toggle payload")
		return
	}
	ports, st := s.gate.setOpen(s.catalog, req.Namespace, req.Service, req.Port, *req.Open)
	if st != nil {
		writePlain(w, st.Code, st.Msg)
		return
	}
	writeJSON(w, http.StatusOK, PortsResponse{Ports: ports})
}

func (s *Server) handleProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, HEAD, POST")
		writePlain(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	trimmed := strings.TrimPrefix(r.URL.Path, ProxyPrefix)
	tok, _, _ := strings.Cut(trimmed, "/")
	pk, ok := s.gate.lookupToken(tok)
	if !ok {
		writePlain(w, http.StatusForbidden, "port is closed in proxy gatekeeper")
		return
	}
	target, st := s.catalog.ResolveTarget(pk.ns, pk.service, pk.port)
	if st != nil {
		writePlain(w, st.Code, st.Msg)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxProxyPostBytes)
	prefix := strings.TrimSuffix(ProxyPrefix, "/") + "/" + tok
	s.newReverseProxy(target, prefix, r.Method == http.MethodHead).ServeHTTP(w, r)
}

func (s *Server) newReverseProxy(target TargetResolved, prefix string, isHead bool) *httputil.ReverseProxy {
	upstreamURL := &url.URL{Scheme: "http", Host: target.Addr}
	return &httputil.ReverseProxy{
		Transport: s.transport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			rest := cleanUpstreamPath(strings.TrimPrefix(pr.In.URL.Path, prefix))
			pr.Out.URL.Scheme = upstreamURL.Scheme
			pr.Out.URL.Host = upstreamURL.Host
			pr.Out.URL.Path = rest
			pr.Out.URL.RawPath = ""
			pr.Out.URL.RawQuery = pr.In.URL.RawQuery
			pr.Out.Host = target.Host
			scrubOutboundHeaders(pr.Out.Header)
		},
		ModifyResponse: func(resp *http.Response) error {
			return hardenProxyResponse(resp, prefix, isHead)
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			s.log.Warn("demo proxy upstream error", "target", target.Host, "err", err)
			writePlain(w, http.StatusBadGateway, "upstream Service unreachable")
		},
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writePlain(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.Error(w, msg, code)
}

type portKey struct {
	ns      string
	service string
	port    int32
}

type gatekeeper struct {
	mu      sync.RWMutex
	byPort  map[portKey]string
	byToken map[string]portKey
}

func newGatekeeper() *gatekeeper {
	return &gatekeeper{byPort: map[portKey]string{}, byToken: map[string]portKey{}}
}

func (g *gatekeeper) list(cat Catalog) []PortEntry {
	ports := cat.ExposedPorts()
	g.mu.RLock()
	defer g.mu.RUnlock()
	for i := range ports {
		k := portKey{ns: ports[i].Namespace, service: ports[i].Service, port: ports[i].Port}
		if tok, ok := g.byPort[k]; ok {
			ports[i].Open = true
			ports[i].PathPrefix = ProxyPrefix + tok
		}
	}
	return ports
}

func (g *gatekeeper) setOpen(cat Catalog, ns, service string, port int32, open bool) ([]PortEntry, *StatusErr) {
	if IsProtectedNamespace(ns) {
		return nil, &StatusErr{Code: http.StatusForbidden, Msg: "protected namespace is refused in demo mode"}
	}
	if open && !hasExposedPort(cat.ExposedPorts(), ns, service, port) {
		return nil, &StatusErr{Code: http.StatusNotFound, Msg: "exposed Service port not found"}
	}
	var buf [16]byte
	if open {
		if _, err := rand.Read(buf[:]); err != nil {
			return nil, &StatusErr{Code: http.StatusInternalServerError, Msg: "failed to mint proxy token"}
		}
	}
	k := portKey{ns: ns, service: service, port: port}
	g.mu.Lock()
	if prev, ok := g.byPort[k]; ok {
		delete(g.byToken, prev)
		delete(g.byPort, k)
	}
	if open {
		tok := hex.EncodeToString(buf[:])
		g.byPort[k] = tok
		g.byToken[tok] = k
	}
	g.mu.Unlock()
	return g.list(cat), nil
}

func (g *gatekeeper) lookupToken(tok string) (portKey, bool) {
	if len(tok) != 32 {
		return portKey{}, false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	k, ok := g.byToken[tok]
	return k, ok
}

func (g *gatekeeper) revokeService(ns, service string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for k, tok := range g.byPort {
		if k.ns == ns && k.service == service {
			delete(g.byToken, tok)
			delete(g.byPort, k)
		}
	}
}
