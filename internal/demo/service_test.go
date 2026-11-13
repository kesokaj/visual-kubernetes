package demo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	"github.com/kesokaj/visual-kubernetes/internal/stream"
)

func doSvcTypeGet(h http.Handler, host, id string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/demo/services/type?id="+id, nil)
	req.Host = host
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func doSvcTypePatch(h http.Handler, host, origin, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPatch, "/api/demo/services/type", strings.NewReader(body))
	req.Host = host
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func newServiceTestHarness(t *testing.T, upAddr string, m *model.Model, objs ...runtime.Object) (*fake.Clientset, *ModelCatalog, http.Handler) {
	t.Helper()
	cs := fake.NewClientset(objs...)
	cat := &ModelCatalog{Model: m, InCluster: true}
	var dialer *SafeDialer
	if upAddr != "" {
		dialer = &SafeDialer{
			DialAddr: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, upAddr)
			},
		}
	}
	srv := NewServer(Options{
		Catalog:  cat,
		Services: &KubeServices{Client: cs},
		Dialer:   dialer,
	})
	return cs, cat, srv.Handler()
}

func TestServiceTypeSwitchClearsNodePortsAndRevokesProxyWithoutWatchRace(t *testing.T) {
	upstream := startMockDemoUpstream(t)
	defer upstream.Close()

	allocFalse := false
	lbClass := "example.com/lb"
	workerSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "worker", ResourceVersion: "41"},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: "10.96.12.34",
			Selector:  map[string]string{"app": "worker"},
			Ports: []corev1.ServicePort{{
				Name: "http", Port: 80, TargetPort: intstr.FromInt32(8080), Protocol: corev1.ProtocolTCP,
			}},
		},
	}
	lbSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "web", ResourceVersion: "10"},
		Spec: corev1.ServiceSpec{
			Type:                          corev1.ServiceTypeLoadBalancer,
			ClusterIP:                     "10.96.12.10",
			Selector:                      map[string]string{"app": "web"},
			ExternalTrafficPolicy:         corev1.ServiceExternalTrafficPolicyLocal,
			HealthCheckNodePort:           32100,
			AllocateLoadBalancerNodePorts: &allocFalse,
			LoadBalancerClass:             &lbClass,
			Ports: []corev1.ServicePort{{
				Name: "http", Port: 80, NodePort: 30080, TargetPort: intstr.FromInt32(8080), Protocol: corev1.ProtocolTCP,
			}},
		},
	}
	extIPSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "extip", ResourceVersion: "5"},
		Spec: corev1.ServiceSpec{
			Type:                  corev1.ServiceTypeLoadBalancer,
			ClusterIP:             "10.96.12.50",
			ExternalIPs:           []string{"203.0.113.50"},
			Selector:              map[string]string{"app": "web"},
			ExternalTrafficPolicy: corev1.ServiceExternalTrafficPolicyLocal,
			HealthCheckNodePort:   32101,
			Ports: []corev1.ServicePort{{
				Name: "http", Port: 80, NodePort: 30081, TargetPort: intstr.FromInt32(8080), Protocol: corev1.ProtocolTCP,
			}},
		},
	}
	pt := networkingv1.PathTypePrefix
	routedSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "routed", ResourceVersion: "20"},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeLoadBalancer,
			ClusterIP: "10.96.12.20",
			Selector:  map[string]string{"app": "web"},
			Ports: []corev1.ServicePort{{
				Name: "http", Port: 80, NodePort: 30082, TargetPort: intstr.FromInt32(8080), Protocol: corev1.ProtocolTCP,
			}},
		},
	}
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "front"},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{{
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path: "/", PathType: &pt,
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{Name: "routed", Port: networkingv1.ServiceBackendPort{Number: 80}},
							},
						}},
					},
				},
			}},
		},
	}

	m := model.New()
	m.Apply(
		model.Change{Obj: workerSvc.DeepCopy()},
		model.Change{Obj: testReadyPod("shop", "worker-0", "worker", "10.244.1.30", 8080, false)},
		model.Change{Obj: lbSvc.DeepCopy()},
		model.Change{Obj: testReadyPod("shop", "web-0", "web", "10.244.1.31", 8080, false)},
		model.Change{Obj: extIPSvc.DeepCopy()},
		model.Change{Obj: routedSvc.DeepCopy()},
		model.Change{Obj: ing},
	)

	cs, _, h := newServiceTestHarness(t, upstream.Listener.Addr().String(), m, workerSvc, lbSvc, extIPSvc, routedSvc)
	rvSeq := 50
	cs.PrependReactor("update", "services", func(act k8stesting.Action) (bool, runtime.Object, error) {
		upd := act.(k8stesting.UpdateAction).GetObject().(*corev1.Service).DeepCopy()
		rvSeq++
		upd.ResourceVersion = fmt.Sprintf("%d", rvSeq)
		_ = cs.Tracker().Update(schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}, upd, upd.Namespace)
		return true, upd, nil
	})

	// 1. GET ClusterIP Service.
	getRec := doSvcTypeGet(h, "localhost:8080", "core/Service/shop/worker")
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET worker = %d %q, want 200", getRec.Code, getRec.Body.String())
	}
	var got ServiceTypeResponse
	if err := json.Unmarshal(getRec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != "ClusterIP" || got.ResourceVersion != "41" || got.ClusterIP != "10.96.12.34" || got.Headless || !slices.Equal(got.AllowedTypes, []string{"ClusterIP", "LoadBalancer"}) {
		t.Fatalf("GET worker payload = %+v", got)
	}

	// 2. Switch worker ClusterIP -> LoadBalancer: immediately appears in /api/demo/ports before Model watch catches up.
	patchLB := doSvcTypePatch(h, "localhost:8080", "http://localhost:8080",
		`{"id":"core/Service/shop/worker","resourceVersion":"41","type":"LoadBalancer"}`)
	if patchLB.Code != http.StatusOK {
		t.Fatalf("PATCH worker -> LB = %d %q, want 200", patchLB.Code, patchLB.Body.String())
	}
	var lbResp ServiceTypeResponse
	_ = json.Unmarshal(patchLB.Body.Bytes(), &lbResp)
	if lbResp.Type != "LoadBalancer" || lbResp.ResourceVersion != "51" {
		t.Fatalf("PATCH worker response = %+v", lbResp)
	}

	openRec := httptest.NewRecorder()
	h.ServeHTTP(openRec, sameOriginPost("/api/demo/ports", `{"namespace":"shop","service":"worker","port":80,"open":true}`))
	if openRec.Code != http.StatusOK {
		t.Fatalf("open worker port immediately after LB switch = %d %q, want 200", openRec.Code, openRec.Body.String())
	}

	// 3. Open web LB port and verify 200, then switch web LB -> ClusterIP (without updating Model!) and verify immediate 403 + cleared fields.
	openWeb := httptest.NewRecorder()
	h.ServeHTTP(openWeb, sameOriginPost("/api/demo/ports", `{"namespace":"shop","service":"web","port":80,"open":true}`))
	if openWeb.Code != http.StatusOK {
		t.Fatalf("open web port = %d %q", openWeb.Code, openWeb.Body.String())
	}
	var portsResp PortsResponse
	_ = json.Unmarshal(openWeb.Body.Bytes(), &portsResp)
	var webPrefix string
	for _, p := range portsResp.Ports {
		if p.Service == "web" {
			webPrefix = p.PathPrefix
		}
	}
	proxyOK := httptest.NewRecorder()
	h.ServeHTTP(proxyOK, loopbackGet(webPrefix+"/"))
	if proxyOK.Code != http.StatusOK {
		t.Fatalf("web proxy before revert = %d, want 200", proxyOK.Code)
	}

	patchCIP := doSvcTypePatch(h, "localhost:8080", "http://localhost:8080",
		`{"id":"core/Service/shop/web","resourceVersion":"10","type":"ClusterIP"}`)
	if patchCIP.Code != http.StatusOK {
		t.Fatalf("PATCH web -> ClusterIP = %d %q, want 200", patchCIP.Code, patchCIP.Body.String())
	}

	// Model still has web as LoadBalancer (rv=10), yet proxy token must be immediately 403 and /api/demo/ports must omit web!
	proxyRevoked := httptest.NewRecorder()
	h.ServeHTTP(proxyRevoked, loopbackGet(webPrefix+"/"))
	if proxyRevoked.Code != http.StatusForbidden {
		t.Errorf("web proxy after revert (before watch) = %d, want 403", proxyRevoked.Code)
	}
	portsAfter := httptest.NewRecorder()
	h.ServeHTTP(portsAfter, loopbackGet("/api/demo/ports"))
	var afterList PortsResponse
	_ = json.Unmarshal(portsAfter.Body.Bytes(), &afterList)
	for _, p := range afterList.Ports {
		if p.Service == "web" {
			t.Errorf("unrouted ClusterIP Service web still listed in /api/demo/ports before watch caught up: %+v", p)
		}
	}

	storedWeb, err := cs.CoreV1().Services("shop").Get(t.Context(), "web", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if storedWeb.Spec.Type != corev1.ServiceTypeClusterIP ||
		storedWeb.Spec.Ports[0].NodePort != 0 ||
		storedWeb.Spec.HealthCheckNodePort != 0 ||
		storedWeb.Spec.AllocateLoadBalancerNodePorts != nil ||
		storedWeb.Spec.LoadBalancerClass != nil ||
		storedWeb.Spec.ExternalTrafficPolicy != "" {
		t.Errorf("stored web spec not cleaned for ClusterIP: %+v", storedWeb.Spec)
	}

	// ExternalIPs Service keeps valid Cluster traffic policy when Local is cleared.
	if rec := doSvcTypePatch(h, "localhost:8080", "http://localhost:8080",
		`{"id":"core/Service/shop/extip","resourceVersion":"5","type":"ClusterIP"}`); rec.Code != http.StatusOK {
		t.Fatalf("PATCH extip -> ClusterIP = %d %q", rec.Code, rec.Body.String())
	}
	storedExtIP, _ := cs.CoreV1().Services("shop").Get(t.Context(), "extip", metav1.GetOptions{})
	if storedExtIP.Spec.ExternalTrafficPolicy != corev1.ServiceExternalTrafficPolicyCluster || storedExtIP.Spec.HealthCheckNodePort != 0 {
		t.Errorf("stored extip spec = %+v, want Cluster traffic policy and 0 healthCheckNodePort", storedExtIP.Spec)
	}

	// 4. Ingress-routed Service keeps its open proxy token when reverted from LoadBalancer -> ClusterIP.
	openRouted := httptest.NewRecorder()
	h.ServeHTTP(openRouted, sameOriginPost("/api/demo/ports", `{"namespace":"shop","service":"routed","port":80,"open":true}`))
	var routedPorts PortsResponse
	_ = json.Unmarshal(openRouted.Body.Bytes(), &routedPorts)
	var routedPrefix string
	for _, p := range routedPorts.Ports {
		if p.Service == "routed" {
			routedPrefix = p.PathPrefix
		}
	}
	if rec := doSvcTypePatch(h, "localhost:8080", "http://localhost:8080",
		`{"id":"core/Service/shop/routed","resourceVersion":"20","type":"ClusterIP"}`); rec.Code != http.StatusOK {
		t.Fatalf("PATCH routed -> ClusterIP = %d %q", rec.Code, rec.Body.String())
	}
	routedCheck := httptest.NewRecorder()
	h.ServeHTTP(routedCheck, loopbackGet(routedPrefix+"/"))
	if routedCheck.Code != http.StatusOK {
		t.Errorf("Ingress-routed Service token revoked on LB->ClusterIP: status = %d, want 200", routedCheck.Code)
	}

	// 5. Once Model observes a strictly newer ResourceVersion (e.g. external kubectl revert back to LoadBalancer), override clears.
	newerLB := storedWeb.DeepCopy()
	newerLB.ResourceVersion = "99"
	newerLB.Spec.Type = corev1.ServiceTypeLoadBalancer
	m.Apply(model.Change{Obj: newerLB})
	portsReopened := httptest.NewRecorder()
	h.ServeHTTP(portsReopened, loopbackGet("/api/demo/ports"))
	var reopenedList PortsResponse
	_ = json.Unmarshal(portsReopened.Body.Bytes(), &reopenedList)
	foundWeb := false
	for _, p := range reopenedList.Ports {
		if p.Service == "web" && p.Exposure == "LoadBalancer" {
			foundWeb = true
		}
	}
	if !foundWeb {
		t.Errorf("ModelCatalog kept stale override after Model reached newer RV 99: %+v", reopenedList.Ports)
	}

	// FieldManager == "visual-kubernetes-demo" and zero Secret actions.
	for _, act := range cs.Actions() {
		if act.GetResource().Resource == "secrets" {
			t.Errorf("unexpected Secret action: %+v", act)
		}
		if u, ok := act.(k8stesting.UpdateActionImpl); ok && u.GetUpdateOptions().FieldManager != FieldManagerName {
			t.Errorf("FieldManager = %q, want %q", u.GetUpdateOptions().FieldManager, FieldManagerName)
		}
	}
}

func TestServiceTypeGuardsValidationAndConflict(t *testing.T) {
	normal := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "worker", ResourceVersion: "9"},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: "10.96.1.2",
			Selector:  map[string]string{"app": "worker"},
			Ports:     []corev1.ServicePort{{Port: 80, Protocol: corev1.ProtocolTCP}},
		},
	}
	headless := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "headless", ResourceVersion: "3"},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: corev1.ClusterIPNone,
			Selector:  map[string]string{"app": "worker"},
			Ports:     []corev1.ServicePort{{Port: 80, Protocol: corev1.ProtocolTCP}},
		},
	}
	extName := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "ext", ResourceVersion: "4"},
		Spec: corev1.ServiceSpec{
			Type:         corev1.ServiceTypeExternalName,
			ExternalName: "db.example.internal",
		},
	}
	selectorless := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "manual", ResourceVersion: "5"},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: "10.96.1.9",
			Ports:     []corev1.ServicePort{{Port: 80, Protocol: corev1.ProtocolTCP}},
		},
	}
	k8sAPISvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "kubernetes", ResourceVersion: "1"},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, ClusterIP: "10.96.0.1"},
	}

	cs, _, h := newServiceTestHarness(t, "", model.New(), normal, headless, extName, selectorless, k8sAPISvc)

	// Protected namespaces & default/kubernetes refused with 403 and zero client calls.
	for _, id := range []string{
		"core/Service/default/kubernetes",
		"core/Service/kube-system/kube-dns",
		"core/Service/kube-public/info",
		"core/Service/kube-node-lease/x",
		"core/Service/visual-kubernetes/visual-kubernetes",
		"core/Service/gke-managed-system/x",
		"core/Service/gmp-system/x",
		"core/Service/_/cluster",
	} {
		if rec := doSvcTypeGet(h, "localhost:8080", id); rec.Code != http.StatusForbidden {
			t.Errorf("GET %s = %d %q, want 403", id, rec.Code, rec.Body.String())
		}
		if rec := doSvcTypePatch(h, "localhost:8080", "http://localhost:8080",
			fmt.Sprintf(`{"id":%q,"resourceVersion":"1","type":"LoadBalancer"}`, id)); rec.Code != http.StatusForbidden {
			t.Errorf("PATCH %s = %d %q, want 403", id, rec.Code, rec.Body.String())
		}
	}
	if len(cs.Actions()) != 0 {
		t.Fatalf("forbidden service requests touched k8s client: %+v", cs.Actions())
	}

	// SameOrigin and LoopbackOrIAP guards.
	if rec := doSvcTypePatch(h, "localhost:8080", "", `{"id":"core/Service/shop/worker","resourceVersion":"9","type":"LoadBalancer"}`); rec.Code != http.StatusForbidden {
		t.Errorf("missing Origin = %d, want 403", rec.Code)
	}
	if rec := doSvcTypeGet(h, "remote.example.com:8080", "core/Service/shop/worker"); rec.Code != http.StatusForbidden {
		t.Errorf("non-loopback Host = %d, want 403", rec.Code)
	}

	// Headless, ExternalName, and selectorless Services return 400 on GET and PATCH.
	for _, id := range []string{
		"core/Service/shop/headless",
		"core/Service/shop/ext",
		"core/Service/shop/manual",
	} {
		if rec := doSvcTypeGet(h, "localhost:8080", id); rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d %q, want 400", id, rec.Code, rec.Body.String())
		}
		if rec := doSvcTypePatch(h, "localhost:8080", "http://localhost:8080",
			fmt.Sprintf(`{"id":%q,"resourceVersion":"3","type":"LoadBalancer"}`, id)); rec.Code != http.StatusBadRequest {
			t.Errorf("PATCH %s = %d %q, want 400", id, rec.Code, rec.Body.String())
		}
	}

	// Malformed / dot-segment requests return 400.
	for _, badID := range []string{
		"core/Service/../worker",
		"core/Service/./worker",
		"core/Service/shop/..",
		"core/Service/shop/.",
	} {
		if rec := doSvcTypeGet(h, "localhost:8080", badID); rec.Code != http.StatusBadRequest {
			t.Errorf("GET dot-segment %s = %d %q, want 400", badID, rec.Code, rec.Body.String())
		}
	}
	for _, body := range []string{
		`{"id":"core/Service/shop/worker","resourceVersion":"9","type":"NodePort"}`,
		`{"id":"core/Service/shop/worker","resourceVersion":"9","type":"ExternalName"}`,
		`{"id":"core/Service/shop/worker","resourceVersion":"","type":"LoadBalancer"}`,
		`{"id":"apps/Deployment/shop/worker","resourceVersion":"9","type":"LoadBalancer"}`,
		`{"id":"app/shop/Deployment/worker","resourceVersion":"9","type":"LoadBalancer"}`,
		`{"id":"core/Service/shop/worker","resourceVersion":"9","type":"LoadBalancer","extra":1}`,
		`{"id":"core/Service/shop/worker","resourceVersion":"9","type":"LoadBalancer"}{}`,
		fmt.Sprintf(`{"id":"core/Service/shop/worker","resourceVersion":%q,"type":"LoadBalancer"}`, strings.Repeat("9", 70*1024)),
	} {
		if rec := doSvcTypePatch(h, "localhost:8080", "http://localhost:8080", body); rec.Code != http.StatusBadRequest {
			t.Errorf("PATCH bad payload = %d %q, want 400", rec.Code, rec.Body.String())
		}
	}

	// Missing Service -> 404.
	if rec := doSvcTypeGet(h, "localhost:8080", "core/Service/shop/missing"); rec.Code != http.StatusNotFound {
		t.Errorf("GET missing = %d, want 404", rec.Code)
	}

	// Stale resourceVersion and API conflict -> 409.
	if rec := doSvcTypePatch(h, "localhost:8080", "http://localhost:8080",
		`{"id":"core/Service/shop/worker","resourceVersion":"8","type":"LoadBalancer"}`); rec.Code != http.StatusConflict {
		t.Errorf("stale rv = %d %q, want 409", rec.Code, rec.Body.String())
	}
	cs.PrependReactor("update", "services", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewConflict(schema.GroupResource{Resource: "services"}, "worker", errors.New("conflict"))
	})
	if rec := doSvcTypePatch(h, "localhost:8080", "http://localhost:8080",
		`{"id":"core/Service/shop/worker","resourceVersion":"9","type":"LoadBalancer"}`); rec.Code != http.StatusConflict {
		t.Errorf("API conflict = %d %q, want 409", rec.Code, rec.Body.String())
	}
}

func TestServiceTypeReplayDeltasAndProxyRevocation(t *testing.T) {
	hub := stream.NewHub(stream.HubOptions{})
	_ = hub.Publish(model.Event{
		Type: stream.EventSnapshot,
		Data: model.SnapshotData{
			Entities: []model.Entity{
				{ID: "app/shop/Deployment/worker", Group: "apps", Kind: "Deployment", Namespace: "shop", Name: "worker", Status: "1/1 ready", OwnerID: "apps/Deployment/shop/worker"},
				{ID: "core/Service/shop/worker", Group: "core", Kind: "Service", Namespace: "shop", Name: "worker", AppID: "app/shop/Deployment/worker", Status: "ClusterIP"},
				{ID: "core/Service/shop/shared", Group: "core", Kind: "Service", Namespace: "shop", Name: "shared", Status: "ClusterIP"},
				{ID: "core/Service/shop/ext", Group: "core", Kind: "Service", Namespace: "shop", Name: "ext", Status: "ExternalName"},
				{ID: "core/Service/default/kubernetes", Group: "core", Kind: "Service", Namespace: "default", Name: "kubernetes", Status: "ClusterIP"},
				{ID: "core/Service/kube-system/kube-dns", Group: "core", Kind: "Service", Namespace: "kube-system", Name: "kube-dns", Status: "ClusterIP"},
			},
		},
	})
	sub, err := hub.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	h := NewReplayServer(hub, false).Handler()

	// 1. Switch shop/worker ClusterIP -> LoadBalancer: ext/internet + routes edge appear!
	rec := doSvcTypePatch(h, "127.0.0.1:18090", "http://127.0.0.1:18090",
		`{"id":"core/Service/shop/worker","resourceVersion":"1","type":"LoadBalancer"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("replay PATCH worker -> LB = %d %q, want 200", rec.Code, rec.Body.String())
	}
	inet, ok := hub.Entity(model.InternetID)
	if !ok || inet.Status != "1 entrypoint" {
		t.Fatalf("after 1st LB, ext/internet = %+v (ok=%v), want 1 entrypoint", inet, ok)
	}
	_, edges := hub.Snapshot()
	if !slices.ContainsFunc(edges, func(e model.Edge) bool { return e.ID == "routes:ext/internet->app/shop/Deployment/worker" }) {
		t.Fatalf("missing routes edge to app/shop/Deployment/worker: %+v", edges)
	}

	// Open worker port through proxy.
	openRec := httptest.NewRecorder()
	openReq := httptest.NewRequest(http.MethodPost, "/api/demo/ports", strings.NewReader(`{"namespace":"shop","service":"worker","port":80,"open":true}`))
	openReq.Host = "127.0.0.1:18090"
	openReq.Header.Set("Origin", "http://127.0.0.1:18090")
	h.ServeHTTP(openRec, openReq)
	if openRec.Code != http.StatusOK {
		t.Fatalf("replay open worker port = %d %q", openRec.Code, openRec.Body.String())
	}
	var opened PortsResponse
	_ = json.Unmarshal(openRec.Body.Bytes(), &opened)
	prefix := opened.Ports[0].PathPrefix

	// 2. Switch standalone shop/shared -> LoadBalancer: ext/internet becomes "2 entrypoints".
	if r2 := doSvcTypePatch(h, "127.0.0.1:18090", "http://127.0.0.1:18090",
		`{"id":"core/Service/shop/shared","resourceVersion":"1","type":"LoadBalancer"}`); r2.Code != http.StatusOK {
		t.Fatalf("replay PATCH shared -> LB = %d %q", r2.Code, r2.Body.String())
	}
	if inet2, ok := hub.Entity(model.InternetID); !ok || inet2.Status != "2 entrypoints" {
		t.Errorf("after 2nd LB, ext/internet = %+v, want 2 entrypoints", inet2)
	}

	// 3. Revert shop/shared -> ClusterIP, then shop/worker -> ClusterIP: token revoked & ext/internet deleted!
	if r3 := doSvcTypePatch(h, "127.0.0.1:18090", "http://127.0.0.1:18090",
		`{"id":"core/Service/shop/shared","resourceVersion":"2","type":"ClusterIP"}`); r3.Code != http.StatusOK {
		t.Fatalf("replay PATCH shared -> ClusterIP = %d %q", r3.Code, r3.Body.String())
	}
	if r4 := doSvcTypePatch(h, "127.0.0.1:18090", "http://127.0.0.1:18090",
		`{"id":"core/Service/shop/worker","resourceVersion":"2","type":"ClusterIP"}`); r4.Code != http.StatusOK {
		t.Fatalf("replay PATCH worker -> ClusterIP = %d %q", r4.Code, r4.Body.String())
	}
	if _, ok := hub.Entity(model.InternetID); ok {
		t.Errorf("ext/internet remained in Hub after all entrypoints reverted to ClusterIP")
	}
	_, edgesAfter := hub.Snapshot()
	if len(edgesAfter) != 0 {
		t.Errorf("routes edges remained after revert: %+v", edgesAfter)
	}

	// 4. An unfolded system-namespace entrypoint (matching model/internet.go:addInternet) is preserved across user Service switches.
	_ = hub.Publish(model.Event{
		Type: string(model.OpUpsert),
		Data: model.EntityData{Entity: model.Entity{
			ID: "core/Service/kube-system/ingress-nginx", Group: "core", Kind: "Service", Namespace: "kube-system", Name: "ingress-nginx", Status: "LoadBalancer",
		}},
	})
	_ = hub.Publish(model.Event{
		Type: string(model.OpUpsert),
		Data: model.EntityData{Entity: model.Entity{ID: model.InternetID, Group: "ext", Kind: "Internet", Name: "internet", Status: "1 entrypoint", FilterGroup: model.Ingress}},
	})
	_ = hub.Publish(model.Event{
		Type: string(model.OpEdgeUpsert),
		Data: model.EdgeData{Edge: model.Edge{ID: "routes:ext/internet->core/Service/kube-system/ingress-nginx", Kind: model.EdgeRoutes, From: model.InternetID, To: "core/Service/kube-system/ingress-nginx"}},
	})
	if r5 := doSvcTypePatch(h, "127.0.0.1:18090", "http://127.0.0.1:18090",
		`{"id":"core/Service/shop/worker","resourceVersion":"3","type":"LoadBalancer"}`); r5.Code != http.StatusOK {
		t.Fatalf("replay PATCH worker -> LB with system LB = %d %q", r5.Code, r5.Body.String())
	}
	if inetSys, ok := hub.Entity(model.InternetID); !ok || inetSys.Status != "2 entrypoints" {
		t.Errorf("with system LB + worker LB, ext/internet = %+v, want 2 entrypoints", inetSys)
	}
	if r6 := doSvcTypePatch(h, "127.0.0.1:18090", "http://127.0.0.1:18090",
		`{"id":"core/Service/shop/worker","resourceVersion":"4","type":"ClusterIP"}`); r6.Code != http.StatusOK {
		t.Fatalf("replay PATCH worker -> ClusterIP with system LB = %d %q", r6.Code, r6.Body.String())
	}
	if inetSysAfter, ok := hub.Entity(model.InternetID); !ok || inetSysAfter.Status != "1 entrypoint" {
		t.Errorf("reverting worker deleted system LB ext/internet: %+v (ok=%v)", inetSysAfter, ok)
	}
	_, sysEdges := hub.Snapshot()
	if !slices.ContainsFunc(sysEdges, func(e model.Edge) bool { return e.ID == "routes:ext/internet->core/Service/kube-system/ingress-nginx" }) {
		t.Errorf("system LB routes edge lost after user Service revert: %+v", sysEdges)
	}

	proxyAfter := httptest.NewRecorder()
	pReq := httptest.NewRequest(http.MethodGet, prefix+"/", nil)
	pReq.Host = "127.0.0.1:18090"
	h.ServeHTTP(proxyAfter, pReq)
	if proxyAfter.Code != http.StatusForbidden {
		t.Errorf("replay proxy after ClusterIP revert = %d, want 403", proxyAfter.Code)
	}

	// Stale rv -> 409, default/kubernetes -> 403, ExternalName -> 400.
	if r := doSvcTypePatch(h, "127.0.0.1:18090", "http://127.0.0.1:18090",
		`{"id":"core/Service/shop/worker","resourceVersion":"1","type":"LoadBalancer"}`); r.Code != http.StatusConflict {
		t.Errorf("replay stale rv = %d, want 409", r.Code)
	}
	if r := doSvcTypeGet(h, "127.0.0.1:18090", "core/Service/default/kubernetes"); r.Code != http.StatusForbidden {
		t.Errorf("replay default/kubernetes = %d, want 403", r.Code)
	}
	if r := doSvcTypeGet(h, "127.0.0.1:18090", "core/Service/shop/ext"); r.Code != http.StatusBadRequest {
		t.Errorf("replay ExternalName = %d, want 400", r.Code)
	}
}
