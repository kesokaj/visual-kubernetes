package demo

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	"github.com/kesokaj/visual-kubernetes/internal/stream"
)

func testSvc(ns, name string, typ corev1.ServiceType, clusterIP string, port int32, lbIP string) *corev1.Service {
	s := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: corev1.ServiceSpec{
			Type:      typ,
			ClusterIP: clusterIP,
			Selector:  map[string]string{"app": name},
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Port:       port,
				TargetPort: intstr.FromInt32(port),
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	}
	if lbIP != "" {
		s.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{IP: lbIP}}
	}
	return s
}

func testReadyPod(ns, name, appLabel, podIP string, port int32, hostNet bool) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Labels: map[string]string{"app": appLabel}},
		Spec: corev1.PodSpec{
			HostNetwork: hostNet,
			Containers: []corev1.Container{{
				Name:  "app",
				Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: port, Protocol: corev1.ProtocolTCP}},
			}},
		},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			PodIP:      podIP,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	}
}

func newTestModel() *model.Model {
	m := model.New()
	pt := networkingv1.PathTypePrefix
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "front"},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{{
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path: "/", PathType: &pt,
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{Name: "api", Port: networkingv1.ServiceBackendPort{Number: 8080}},
							},
						}},
					},
				},
			}},
		},
	}
	route := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "HTTPRoute",
		"metadata":   map[string]any{"namespace": "shop", "name": "checkout-route"},
		"spec": map[string]any{
			"rules": []any{map[string]any{"backendRefs": []any{map[string]any{"name": "checkout", "port": int64(8081)}}}},
		},
	}}
	selectorless := testSvc("shop", "manual-ep", corev1.ServiceTypeLoadBalancer, "10.96.1.99", 80, "203.0.113.99")
	selectorless.Spec.Selector = nil
	kubeletTarget := testSvc("shop", "sneaky-kubelet", corev1.ServiceTypeLoadBalancer, "10.96.1.98", 80, "")
	kubeletTarget.Spec.Ports[0].TargetPort = intstr.FromInt32(10250)
	webSvc := testSvc("shop", "web", corev1.ServiceTypeLoadBalancer, "10.96.1.10", 80, "203.0.113.20")
	webSvc.Spec.Ports[0].TargetPort = intstr.FromString("http")

	m.Apply(
		model.Change{Obj: &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "w1"},
			Status:     corev1.NodeStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "192.168.1.10"}}},
		}},
		model.Change{Obj: testSvc("default", "kubernetes", corev1.ServiceTypeClusterIP, "10.96.0.1", 443, "")},
		model.Change{Obj: webSvc},
		model.Change{Obj: testReadyPod("shop", "web-0", "web", "10.244.1.25", 8080, false)},
		model.Change{Obj: testSvc("shop", "api", corev1.ServiceTypeClusterIP, "10.96.1.11", 8080, "")},
		model.Change{Obj: testReadyPod("shop", "api-0", "api", "169.254.169.254", 8080, false)},
		model.Change{Obj: testSvc("shop", "checkout", corev1.ServiceTypeClusterIP, "10.96.1.12", 8081, "")},
		model.Change{Obj: testReadyPod("shop", "checkout-0", "checkout", "192.168.1.10", 8081, true)},
		model.Change{Obj: testSvc("shop", "internal-only", corev1.ServiceTypeClusterIP, "10.96.1.13", 8082, "")},
		model.Change{Obj: testSvc("shop", "headless", corev1.ServiceTypeNodePort, "None", 8083, "")},
		model.Change{Obj: selectorless},
		model.Change{Obj: kubeletTarget},
		model.Change{Obj: testSvc("kube-system", "kube-dns", corev1.ServiceTypeNodePort, "10.96.0.10", 53, "")},
		model.Change{Obj: ing},
		model.Change{Obj: route},
	)
	return m
}

func sameOriginPost(path, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Origin", "http://127.0.0.1:8080")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Content-Type", "application/json")
	return req
}

func loopbackGet(path string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Host = "127.0.0.1:8080"
	return req
}

func TestDemoProxyCatalogAndTokenLifecycle(t *testing.T) {
	m := newTestModel()
	h := NewServer(Options{Catalog: &ModelCatalog{Model: m, InCluster: true}}).Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, loopbackGet("/api/demo/ports"))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/demo/ports = %d %q", rec.Code, rec.Body.String())
	}
	var got PortsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Ports) != 4 {
		t.Fatalf("ports = %+v, want 4 exposed non-system ports", got.Ports)
	}
	for _, p := range got.Ports {
		if p.Open || p.PathPrefix != "" || p.Namespace == "kube-system" || p.Service == "internal-only" {
			t.Errorf("unexpected initial port state: %+v", p)
		}
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, loopbackGet("/api/demo/proxy/t/00000000000000000000000000000000/"))
	if rec.Code != http.StatusForbidden {
		t.Errorf("unknown token status = %d, want 403", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, sameOriginPost("/api/demo/ports", `{"namespace":"kube-system","service":"kube-dns","port":53,"open":true}`))
	if rec.Code != http.StatusForbidden {
		t.Errorf("protected ns open = %d, want 403", rec.Code)
	}

	badPost := httptest.NewRequest(http.MethodPost, "/api/demo/ports", strings.NewReader(`{"namespace":"shop","service":"web","port":80,"open":true}`))
	badPost.Host = "127.0.0.1:8080"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, badPost)
	if rec.Code != http.StatusForbidden {
		t.Errorf("missing Origin POST = %d, want 403", rec.Code)
	}
}

func startMockDemoUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Goog-Iap-Jwt-Assertion") != "" {
			t.Errorf("presenter credentials leaked to upstream: %v", r.Header)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Set-Cookie", "session=steal; Path=/")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Content-Security-Policy", "default-src *")
			w.Header().Set("Content-Encoding", "gzip")
			var gz bytes.Buffer
			zw := gzip.NewWriter(&gz)
			_, _ = io.WriteString(zw, `<html><body><a href="/about">About</a><a href=/unquoted>Unq</a><a href="/api/demo/proxy/t/../../../../api/objects?id=x">Esc1</a><a href="../../escape">Esc2</a><a href="//evil.example/x">Ext</a><form action="/submit" method="post"></form><div data-href="/keep"></div></body></html>`)
			_ = zw.Close()
			_, _ = w.Write(gz.Bytes())
		case r.Method == http.MethodPost && r.URL.Path == "/submit":
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, `<p>submitted</p>`)
		case r.Method == http.MethodGet && r.URL.Path == "/redir":
			w.Header().Set("Location", "/landed")
			w.WriteHeader(http.StatusFound)
		case r.Method == http.MethodGet && r.URL.Path == "/rel-escape":
			w.Header().Set("Location", "../../../../api/logs?id=secret")
			w.WriteHeader(http.StatusFound)
		case r.Method == http.MethodGet && r.URL.Path == "/rel-login":
			w.Header().Set("Location", "../login")
			w.WriteHeader(http.StatusFound)
		case r.Method == http.MethodGet && r.URL.Path == "/ext-redir":
			w.Header().Set("Location", "https://evil.example/phish")
			w.WriteHeader(http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestDemoProxyOpenRewriteSandboxAndRevoke(t *testing.T) {
	upstream := startMockDemoUpstream(t)
	defer upstream.Close()
	upAddr := upstream.Listener.Addr().String()

	m := newTestModel()
	dialer := &SafeDialer{
		DialAddr: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, upAddr)
		},
	}
	h := NewServer(Options{Catalog: &ModelCatalog{Model: m, InCluster: true}, Dialer: dialer}).Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, sameOriginPost("/api/demo/ports", `{"namespace":"shop","service":"web","port":80,"open":true}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("open port = %d %q", rec.Code, rec.Body.String())
	}
	var opened PortsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &opened)
	var prefix string
	for _, p := range opened.Ports {
		if p.Service == "web" && p.Open {
			prefix = p.PathPrefix
		}
	}
	if !strings.HasPrefix(prefix, "/api/demo/proxy/t/") || len(strings.TrimPrefix(prefix, "/api/demo/proxy/t/")) != 32 {
		t.Fatalf("bad pathPrefix = %q", prefix)
	}

	verifyProxiedHTMLAndHeaders(t, h, prefix)
	verifyOpaqueOriginPostAndRedirects(t, h, prefix)

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, sameOriginPost("/api/demo/ports", `{"namespace":"shop","service":"web","port":80,"open":false}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("close port = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, loopbackGet(prefix+"/"))
	if rec.Code != http.StatusForbidden {
		t.Errorf("revoked token status = %d, want 403", rec.Code)
	}
}

func verifyProxiedHTMLAndHeaders(t *testing.T, h http.Handler, prefix string) {
	t.Helper()
	req := loopbackGet(prefix + "/")
	req.Header.Set("Cookie", "presenter=secret")
	req.Header.Set("Authorization", "Bearer topsecret")
	req.Header.Set("X-Goog-Iap-Jwt-Assertion", "jwt")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET proxied / = %d %q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Set-Cookie") != "" || rec.Header().Get("X-Frame-Options") != "" {
		t.Errorf("dangerous upstream headers survived: %v", rec.Header())
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "sandbox allow-scripts allow-forms") || !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("CSP = %q, want sandbox + default-src 'self'", csp)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `href="`+prefix+`/about"`) || !strings.Contains(body, `action="`+prefix+`/submit"`) {
		t.Errorf("root-relative HTML paths not rewritten: %q", body)
	}
	if !strings.Contains(body, `href="`+prefix+`/unquoted"`) ||
		!strings.Contains(body, `href="`+prefix+`/api/objects?id=x"`) ||
		!strings.Contains(body, `href="`+prefix+`/escape"`) {
		t.Errorf("unquoted or dot-segment HTML paths escaped token prefix: %q", body)
	}
	if !strings.Contains(body, `href="//evil.example/x"`) || !strings.Contains(body, `data-href="/keep"`) {
		t.Errorf("protocol-relative or data-href wrongly rewritten: %q", body)
	}
}

func verifyOpaqueOriginPostAndRedirects(t *testing.T, h http.Handler, prefix string) {
	t.Helper()
	postReq := httptest.NewRequest(http.MethodPost, prefix+"/submit", strings.NewReader("a=1"))
	postReq.Host = "127.0.0.1:8080"
	postReq.Header.Set("Origin", "null")
	postReq.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, postReq)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "submitted") {
		t.Errorf("opaque-origin POST with valid token = %d %q, want 200", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, loopbackGet(prefix+"/redir"))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != prefix+"/landed" {
		t.Errorf("302 rewrite = %d Location=%q, want 302 %s/landed", rec.Code, rec.Header().Get("Location"), prefix)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, loopbackGet(prefix+"/rel-escape"))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != prefix+"/api/logs?id=secret" {
		t.Errorf("relative escape 302 Location = %q, want clamped %s/api/logs?id=secret", rec.Header().Get("Location"), prefix)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, loopbackGet(prefix+"/rel-login"))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != prefix+"/login" {
		t.Errorf("relative ../login 302 Location = %q, want %s/login", rec.Header().Get("Location"), prefix)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, loopbackGet(prefix+"/ext-redir"))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "" {
		t.Errorf("external 302 Location = %q, want stripped", rec.Header().Get("Location"))
	}
}

func TestDemoProxySafeDialerAndOutOfClusterTarget(t *testing.T) {
	d := &SafeDialer{
		DialAddr: func(_ context.Context, _, _ string) (net.Conn, error) {
			t.Fatal("SafeDialer dialed a forbidden target")
			return nil, nil
		},
		ForbiddenIPs: func() []netip.Addr {
			return []netip.Addr{netip.MustParseAddr("10.96.0.1")}
		},
	}
	forbidden := []string{
		"127.0.0.1:80", "[::1]:80", "[::ffff:127.0.0.1]:80",
		"169.254.169.254:80", "[::ffff:169.254.169.254]:80",
		"[fe80::1]:80", "[fd00:ec2::254]:80", "0.0.0.0:80", "[::]:80", "224.0.0.1:80",
		"10.96.1.10:10250", "10.96.1.10:10255", "10.96.0.1:443", "kubernetes.default.svc:443",
	}
	for _, addr := range forbidden {
		if _, err := d.DialContext(context.Background(), "tcp", addr); err == nil {
			t.Errorf("SafeDialer allowed %q, want error", addr)
		}
	}

	m := newTestModel()
	outCat := &ModelCatalog{Model: m, InCluster: false}
	if _, st := outCat.ResolveTarget("shop", "api", 8080); st == nil || st.Code != http.StatusBadGateway {
		t.Errorf("out-of-cluster ClusterIP = %+v, want 502", st)
	}
	if tgt, st := outCat.ResolveTarget("shop", "web", 80); st != nil || tgt.Addr != "203.0.113.20:80" {
		t.Errorf("out-of-cluster LB IP = %+v %+v, want 203.0.113.20:80", tgt, st)
	}
	inCat := &ModelCatalog{Model: m, InCluster: true}
	if tgt, st := inCat.ResolveTarget("shop", "web", 80); st != nil || tgt.Addr != "10.244.1.25:8080" {
		t.Errorf("in-cluster validated Pod target = %+v %+v, want 10.244.1.25:8080 (not virtual ClusterIP)", tgt, st)
	}
	if _, st := inCat.ResolveTarget("shop", "manual-ep", 80); st == nil || st.Code != http.StatusForbidden {
		t.Errorf("selectorless Service = %+v, want 403", st)
	}
	if _, st := inCat.ResolveTarget("shop", "sneaky-kubelet", 80); st == nil || st.Code != http.StatusForbidden {
		t.Errorf("Service targeting 10250 = %+v, want 403", st)
	}
	if _, st := inCat.ResolveTarget("shop", "api", 8080); st == nil || st.Code != http.StatusBadGateway {
		t.Errorf("Service whose PodIP is 169.254.169.254 = %+v, want 502", st)
	}
	if _, st := inCat.ResolveTarget("shop", "checkout", 8081); st == nil || st.Code != http.StatusBadGateway {
		t.Errorf("Service whose Pod uses hostNetwork/nodeIP = %+v, want 502", st)
	}
	if _, st := inCat.ResolveTarget("shop", "headless", 8083); st == nil || st.Code != http.StatusBadGateway {
		t.Errorf("headless Service = %+v, want 502", st)
	}
}

func TestDemoProxyReplayHandler(t *testing.T) {
	hub := stream.NewHub(stream.HubOptions{})
	_ = hub.Publish(model.Event{
		Type: stream.EventSnapshot,
		Data: model.SnapshotData{
			Entities: []model.Entity{
				{ID: "core/Service/shop/web", Group: "core", Kind: "Service", Namespace: "shop", Name: "web", AppID: "app/shop/Deployment/web", Status: "LoadBalancer"},
				{ID: "core/Service/kube-system/kube-dns", Group: "core", Kind: "Service", Namespace: "kube-system", Name: "kube-dns", Status: "ClusterIP"},
			},
		},
	})
	h := NewReplayServer(hub, false).Handler()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, sameOriginPost("/api/demo/ports", `{"namespace":"shop","service":"web","port":80,"open":true}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("replay open = %d %q", rec.Code, rec.Body.String())
	}
	var opened PortsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &opened)
	if len(opened.Ports) != 1 || !opened.Ports[0].Open {
		t.Fatalf("replay ports = %+v", opened.Ports)
	}
	prefix := opened.Ports[0].PathPrefix

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, loopbackGet(prefix+"/"))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), prefix+"/about") {
		t.Fatalf("replay GET / = %d %q", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, loopbackGet(prefix+"/about"))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "About") {
		t.Fatalf("replay GET /about = %d %q", rec.Code, rec.Body.String())
	}
	echoReq := httptest.NewRequest(http.MethodPost, prefix+"/echo", strings.NewReader(url.Values{"msg": {"hi <script>"}}.Encode()))
	echoReq.Host = "127.0.0.1:8080"
	echoReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	echoReq.Header.Set("Origin", "null")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, echoReq)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "hi &lt;script&gt;") {
		t.Fatalf("replay POST /echo = %d %q", rec.Code, rec.Body.String())
	}
}
