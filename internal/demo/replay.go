package demo

import (
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	"github.com/kesokaj/visual-kubernetes/internal/stream"
)

// NewReplayServer builds a demo Server backed by the recorded stream in hub and a deterministic
// in-memory HTML upstream (D10).
func NewReplayServer(hub *stream.Hub) *Server {
	return NewServer(Options{
		Catalog:   &ReplayCatalog{Hub: hub},
		Transport: ReplayTransport(),
	})
}

// ReplayCatalog derives exposed non-protected Service ports from a replay Hub's entities and edges.
type ReplayCatalog struct {
	Hub *stream.Hub
}

// ExposedPorts returns port 80 ("http") for every unfolded non-protected Service in Hub that is
// LoadBalancer, NodePort, or routed by an unfolded Ingress or HTTPRoute.
func (c *ReplayCatalog) ExposedPorts() []PortEntry {
	if c == nil || c.Hub == nil {
		return []PortEntry{}
	}
	ents, edges := c.Hub.Snapshot()
	routedSvc, routedApp := replayRoutedTargets(ents, edges)
	out := make([]PortEntry, 0)
	for _, e := range ents {
		if e.Group != "core" || e.Kind != "Service" || e.Folded || IsProtectedNamespace(e.Namespace) {
			continue
		}
		exp := replayServiceExposure(e, routedSvc, routedApp)
		if exp == "" {
			continue
		}
		out = append(out, PortEntry{
			ID: e.ID, Namespace: e.Namespace, Service: e.Name, AppID: e.AppID,
			Port: 80, Name: "http", Exposure: exp,
		})
	}
	return out
}

func replayRoutedTargets(ents []model.Entity, edges []model.Edge) (bySvc, byApp map[string]string) {
	bySvc = map[string]string{}
	byApp = map[string]string{}
	ingressByID := map[string]model.Entity{}
	for _, e := range ents {
		if e.Folded {
			continue
		}
		if e.Kind == "Ingress" || e.Kind == "HTTPRoute" {
			ingressByID[e.ID] = e
			if e.AppID != "" && byApp[e.AppID] == "" {
				byApp[e.AppID] = e.Kind
			}
		}
	}
	for _, edge := range edges {
		if edge.Kind != model.EdgeRoutes {
			continue
		}
		src, ok := ingressByID[edge.From]
		if !ok {
			continue
		}
		if strings.HasPrefix(edge.To, "core/Service/") && bySvc[edge.To] == "" {
			bySvc[edge.To] = src.Kind
		} else if strings.HasPrefix(edge.To, "app/") && byApp[edge.To] == "" {
			byApp[edge.To] = src.Kind
		}
	}
	return bySvc, byApp
}

func replayServiceExposure(e model.Entity, bySvc, byApp map[string]string) string {
	switch e.Status {
	case "LoadBalancer", "NodePort":
		return e.Status
	}
	if exp := bySvc[e.ID]; exp != "" {
		return exp
	}
	if e.AppID != "" {
		return byApp[e.AppID]
	}
	return ""
}

// ResolveTarget returns a synthetic replay target for an exposed Service port.
func (c *ReplayCatalog) ResolveTarget(ns, service string, port int32) (TargetResolved, *StatusErr) {
	if IsProtectedNamespace(ns) || !hasExposedPort(c.ExposedPorts(), ns, service, port) {
		return TargetResolved{}, &StatusErr{Code: http.StatusForbidden, Msg: "port is closed in proxy gatekeeper"}
	}
	return TargetResolved{
		Addr: "replay.internal:80",
		Host: fmt.Sprintf("%s.%s.svc:%d", service, ns, port),
	}, nil
}

// ForbiddenIPs returns nil in replay mode because ReplayTransport never opens network sockets.
func (c *ReplayCatalog) ForbiddenIPs() []netip.Addr { return nil }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// ReplayTransport serves a deterministic, self-contained HTML demo app (/, /about, POST /echo)
// with zero network access so offline Playwright tests exercise the full proxy & sandbox pipeline.
func ReplayTransport() http.RoundTripper {
	handler := http.HandlerFunc(serveReplayDemoPage)
	return roundTripFunc(func(r *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		return rec.Result(), nil
	})
}

func serveReplayDemoPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	svcLabel := html.EscapeString(strings.TrimSuffix(r.Host, ":80"))
	if svcLabel == "" {
		svcLabel = "demo-service"
	}
	const style = `<style>body{margin:16px;font:14px/1.5 system-ui,sans-serif;background:#fff;color:#111827}h1{font-size:18px;margin:0 0 8px}a,button,input,label,p{font-size:14px}form{display:flex;gap:8px;align-items:center;margin-top:12px}input{border:1px solid #d1d5db;border-radius:6px;padding:4px 8px}button{border:1px solid #2563eb;background:#eff6ff;color:#1d4ed8;border-radius:6px;padding:4px 10px;cursor:pointer}</style>`
	switch {
	case (r.Method == http.MethodGet || r.Method == http.MethodHead) && r.URL.Path == "/":
		_, _ = fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><title>%s</title>%s</head><body><h1>%s</h1><p>Interactive demo preview served through the controlled Service proxy.</p><p><a id="nav-about" href="/about">About service</a></p><form id="echo-form" method="post" action="/echo"><label for="msg">Message</label><input id="msg" name="msg" value="hello cluster"><button id="echo-submit" type="submit">Send</button></form></body></html>`, svcLabel, style, svcLabel)
	case (r.Method == http.MethodGet || r.Method == http.MethodHead) && r.URL.Path == "/about":
		_, _ = fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><title>About %s</title>%s</head><body><h1>About %s</h1><p id="about-copy">Deterministic replay upstream for non-production teaching demos.</p><p><a id="nav-home" href="/">Back home</a></p></body></html>`, svcLabel, style, svcLabel)
	case r.Method == http.MethodPost && r.URL.Path == "/echo":
		_ = r.ParseForm()
		msg := html.EscapeString(r.FormValue("msg"))
		_, _ = fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><title>Echo</title>%s</head><body><h1>Echo response</h1><p id="echo-out">Echo: %s</p><p><a id="nav-home" href="/">Back home</a></p></body></html>`, style, msg)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8">%s</head><body><p>Not found</p><p><a href="/">Back home</a></p></body></html>`, style)
	}
}
