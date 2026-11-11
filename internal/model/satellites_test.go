package model_test

import (
	"slices"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	mt "github.com/kesokaj/visual-kubernetes/internal/model/modeltest"
)

func TestConfigMapSatelliteSharedAndCounted(t *testing.T) {
	m := model.New()
	apply(m,
		withRootCA(mt.Deployment("demo", "a", 1, mt.Labels("app", "a"), "only-a", "shared")),
		withRootCA(mt.Deployment("demo", "b", 1, mt.Labels("app", "b"), "shared")),
		mt.ConfigMap("demo", "only-a"), mt.ConfigMap("demo", "shared"),
		mt.ConfigMap("demo", "kube-root-ca.crt"), mt.ConfigMap("demo", "unused"),
		mt.ServiceAccount("demo", "default"), mt.ServiceAccount("demo", "builder"),
	)
	if got := mustEntity(t, m, "core/ConfigMap/demo/only-a").AppID; got != "app/demo/Deployment/a" {
		t.Errorf("only-a appId = %q, want app/demo/Deployment/a", got)
	}
	want := []string{
		"references:core/ConfigMap/demo/shared->app/demo/Deployment/a",
		"references:core/ConfigMap/demo/shared->app/demo/Deployment/b",
	}
	if got := edgeIDs(m, model.EdgeReferences); !slices.Equal(got, want) {
		t.Errorf("references = %v, want %v", got, want)
	}
	ents := entities(m)
	for _, id := range []string{"core/ConfigMap/demo/unused", "core/ConfigMap/demo/kube-root-ca.crt", "core/ServiceAccount/demo/default"} {
		if _, ok := ents[id]; ok {
			t.Errorf("%s is drawn, want it only counted", id)
		}
	}
	if c := mustEntity(t, m, "counter/core.ConfigMap"); c.Count != 2 {
		t.Errorf("ConfigMap counter = %d, want 2 (unused and kube-root-ca.crt)", c.Count)
	}
	if c := mustEntity(t, m, "counter/core.ServiceAccount"); c.Count != 2 || c.FilterGroup != model.System {
		t.Errorf("ServiceAccount counter = %+v, want count 2 in system", c)
	}
}

// withRootCA adds the projected kube-root-ca.crt volume the ServiceAccount admission plugin
// puts in every pod, so the test proves it never counts as a reference.
func withRootCA(d *appsv1.Deployment) *appsv1.Deployment {
	d.Spec.Template.Spec.Volumes = append(d.Spec.Template.Spec.Volumes, corev1.Volume{
		Name: "kube-api-access",
		VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{Sources: []corev1.VolumeProjection{
			{ServiceAccountToken: &corev1.ServiceAccountTokenProjection{Path: "token"}},
			{ConfigMap: &corev1.ConfigMapProjection{LocalObjectReference: corev1.LocalObjectReference{Name: "kube-root-ca.crt"}}},
		}}},
	})
	return d
}

func TestPVCReferencedByPod(t *testing.T) {
	m := model.New()
	p := mt.Pod("db", "pg-0", mt.Labels("app", "pg"), nil)
	p.Spec.Volumes = []corev1.Volume{{Name: "data", VolumeSource: corev1.VolumeSource{
		PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data-pg-0"},
	}}}
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Namespace: "db", Name: "data-pg-0"},
		Status:     corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound},
	}
	orphan := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Namespace: "db", Name: "orphan"}}
	apply(m, p, pvc, orphan)
	if e := mustEntity(t, m, "core/PersistentVolumeClaim/db/data-pg-0"); e.AppID != "app/db/Pod/pg-0" || e.Status != "Bound" {
		t.Errorf("pvc = %+v, want satellite of app/db/Pod/pg-0, Bound", e)
	}
	if e := mustEntity(t, m, "core/PersistentVolumeClaim/db/orphan"); e.AppID != "" {
		t.Errorf("orphan pvc appId = %q, want standalone", e.AppID)
	}
}

func TestNetworkPolicySelection(t *testing.T) {
	m := model.New()
	apply(m,
		mt.Deployment("shop", "front", 1, mt.Labels("app", "front")),
		mt.Deployment("shop", "cart", 1, mt.Labels("app", "cart")),
		mt.Deployment("other", "x", 1, mt.Labels("app", "front")),
		mt.NetworkPolicy("shop", "cart-only", mt.Labels("app", "cart")),
		mt.NetworkPolicy("shop", "default-deny", nil),
	)
	if got := mustEntity(t, m, "networking.k8s.io/NetworkPolicy/shop/cart-only").AppID; got != "app/shop/Deployment/cart" {
		t.Errorf("cart-only appId = %q, want app/shop/Deployment/cart", got)
	}
	want := []string{
		"selects:networking.k8s.io/NetworkPolicy/shop/default-deny->app/shop/Deployment/cart",
		"selects:networking.k8s.io/NetworkPolicy/shop/default-deny->app/shop/Deployment/front",
	}
	if got := edgeIDs(m, model.EdgeSelects); !slices.Equal(got, want) {
		t.Errorf("selects = %v, want %v (empty selector: every app in its namespace only)", got, want)
	}
}

func TestIngressRoutesToAppOrService(t *testing.T) {
	m := model.New()
	apply(m,
		mt.Deployment("shop", "front", 1, mt.Labels("app", "front")),
		mt.Deployment("shop", "a", 1, mt.Labels("tier", "be")),
		mt.Deployment("shop", "b", 1, mt.Labels("tier", "be")),
		mt.Service("shop", "front", mt.Labels("app", "front")),
		mt.Service("shop", "be", mt.Labels("tier", "be")),
		mt.Ingress("shop", "web", "front"),
		mt.Ingress("shop", "api", "be"),
		mt.Ingress("shop", "dangling", "missing"),
	)
	if e := mustEntity(t, m, "networking.k8s.io/Ingress/shop/web"); e.AppID != "app/shop/Deployment/front" || e.FilterGroup != model.Ingress {
		t.Errorf("web ingress = %+v, want satellite of app/shop/Deployment/front in ingress", e)
	}
	// Every Ingress is an entrypoint, so ext/internet routes to the app or the Ingress itself.
	want := []string{
		"routes:ext/internet->app/shop/Deployment/front",
		"routes:ext/internet->networking.k8s.io/Ingress/shop/api",
		"routes:ext/internet->networking.k8s.io/Ingress/shop/dangling",
		"routes:networking.k8s.io/Ingress/shop/api->core/Service/shop/be",
	}
	if got := edgeIDs(m, model.EdgeRoutes); !slices.Equal(got, want) {
		t.Errorf("routes = %v, want %v", got, want)
	}
	if e := mustEntity(t, m, "networking.k8s.io/Ingress/shop/dangling"); e.AppID != "" {
		t.Errorf("dangling ingress appId = %q, want standalone", e.AppID)
	}
}

func unstructuredObj(apiVersion, kind, ns, name string, spec map[string]any) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{"spec": spec}}
	u.SetAPIVersion(apiVersion)
	u.SetKind(kind)
	u.SetNamespace(ns)
	u.SetName(name)
	return u
}

func TestUnstructuredVPAGatewayAndHTTPRoute(t *testing.T) {
	m := model.New()
	apply(m,
		mt.Deployment("shop", "front", 1, mt.Labels("app", "front")),
		mt.Service("shop", "front", mt.Labels("app", "front")),
		unstructuredObj("autoscaling.k8s.io/v1", "VerticalPodAutoscaler", "shop", "front", map[string]any{
			"targetRef":    map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "front"},
			"updatePolicy": map[string]any{"updateMode": "Auto"},
		}),
		unstructuredObj("gateway.networking.k8s.io/v1", "Gateway", "shop", "gw", map[string]any{"gatewayClassName": "gke-l7"}),
		unstructuredObj("gateway.networking.k8s.io/v1", "HTTPRoute", "shop", "front", map[string]any{
			"parentRefs": []any{map[string]any{"name": "gw"}, map[string]any{"name": "absent"}},
			"rules":      []any{map[string]any{"backendRefs": []any{map[string]any{"name": "front", "port": int64(80)}}}},
		}),
	)
	if e := mustEntity(t, m, "autoscaling.k8s.io/VerticalPodAutoscaler/shop/front"); e.AppID != "app/shop/Deployment/front" || e.Status != "Auto" {
		t.Errorf("vpa = %+v, want satellite of app/shop/Deployment/front, Auto", e)
	}
	if e := mustEntity(t, m, "gateway.networking.k8s.io/HTTPRoute/shop/front"); e.AppID != "app/shop/Deployment/front" {
		t.Errorf("route appId = %q, want app/shop/Deployment/front", e.AppID)
	}
	if e := mustEntity(t, m, "gateway.networking.k8s.io/Gateway/shop/gw"); e.FilterGroup != model.Ingress || e.Status != "gke-l7" {
		t.Errorf("gateway = %+v, want ingress group with its class", e)
	}
	want := []string{"parent:gateway.networking.k8s.io/Gateway/shop/gw->gateway.networking.k8s.io/HTTPRoute/shop/front"}
	if got := edgeIDs(m, model.EdgeParent); !slices.Equal(got, want) {
		t.Errorf("parent edges = %v, want %v (absent gateway has no edge)", got, want)
	}
}

func TestSecretsAndEventsAreNeverStored(t *testing.T) {
	m := model.New()
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "demo", Name: "token"}}
	event := &corev1.Event{ObjectMeta: metav1.ObjectMeta{Namespace: "demo", Name: "e1"}}
	if d := apply(m, secret, event); d != nil {
		t.Errorf("deltas = %v, want none", d)
	}
	if _, ok := m.Object("core/Secret/demo/token"); ok {
		t.Error("a Secret was stored (R2)")
	}
	if _, ok := entities(m)["counter/core.Secret"]; ok {
		t.Error("Secrets are counted (R2: never read, so never counted)")
	}
}
