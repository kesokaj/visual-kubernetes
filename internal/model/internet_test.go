package model_test

import (
	"encoding/json"
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	mt "github.com/kesokaj/visual-kubernetes/internal/model/modeltest"
)

// exposed returns a Service of the given type selecting app=name.
func exposed(ns, name string, typ corev1.ServiceType) *corev1.Service {
	s := mt.Service(ns, name, mt.Labels("app", name))
	s.Spec.Type = typ
	return s
}

func TestInternetOnlyWithEntrypoint(t *testing.T) {
	m := model.New()
	apply(m, mt.Deployment("shop", "front", 1, mt.Labels("app", "front")),
		mt.Service("shop", "front", mt.Labels("app", "front")))
	if _, ok := entities(m)[model.InternetID]; ok {
		t.Fatalf("%s drawn with only a ClusterIP Service, want no globe", model.InternetID)
	}

	apply(m, exposed("shop", "front", corev1.ServiceTypeLoadBalancer))
	e := mustEntity(t, m, model.InternetID)
	if e.Kind != "Internet" || e.Group != "ext" || e.FilterGroup != model.Ingress || e.Status != "1 entrypoint" {
		t.Errorf("internet = %+v, want Kind Internet, group ext, filter ingress, 1 entrypoint", e)
	}
	want := []string{"routes:ext/internet->app/shop/Deployment/front"}
	if got := edgeIDs(m, model.EdgeRoutes); !slices.Equal(got, want) {
		t.Errorf("routes = %v, want %v", got, want)
	}
}

func TestInternetRoutesToEachEntrypoint(t *testing.T) {
	m := model.New()
	apply(m,
		mt.Deployment("shop", "front", 1, mt.Labels("app", "front")),
		mt.Deployment("shop", "api", 1, mt.Labels("app", "api")),
		mt.Service("shop", "front", mt.Labels("app", "front")),
		mt.Ingress("shop", "web", "front"),
		exposed("shop", "api", corev1.ServiceTypeNodePort),
		unstructuredObj("gateway.networking.k8s.io/v1", "Gateway", "shop", "gw", map[string]any{"gatewayClassName": "gke-l7"}),
	)
	want := []string{
		"routes:ext/internet->app/shop/Deployment/api",
		"routes:ext/internet->app/shop/Deployment/front",
		"routes:ext/internet->gateway.networking.k8s.io/Gateway/shop/gw",
	}
	var got []string
	for _, id := range edgeIDs(m, model.EdgeRoutes) {
		if len(id) > 19 && id[:19] == "routes:ext/internet" {
			got = append(got, id)
		}
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("internet routes = %v, want %v", got, want)
	}
	if s := mustEntity(t, m, model.InternetID).Status; s != "3 entrypoints" {
		t.Errorf("status = %q, want 3 entrypoints", s)
	}
}

func TestInternetSkipsFoldedTargets(t *testing.T) {
	m := model.New()
	objs := bigCluster(600) // kube-system apps always fold beyond the S5 limits
	lb := mt.Service("kube-system", "kube-proxy", mt.Labels("k8s-app", "kube-proxy"))
	lb.Spec.Type = corev1.ServiceTypeLoadBalancer
	objs = append(objs, lb)
	apply(m, objs...)
	if !entities(m)["app/kube-system/DaemonSet/kube-proxy"].Folded {
		t.Fatal("precondition: kube-proxy app is not folded")
	}
	if e, ok := entities(m)[model.InternetID]; ok {
		t.Errorf("%s drawn = %+v, want none when every entrypoint is folded", model.InternetID, e)
	}
}

// TestInternetJSON pins the wire shape test/e2e/fixtures/traffic.mjs copies.
func TestInternetJSON(t *testing.T) {
	m := model.New()
	apply(m,
		mt.Deployment("shop", "front", 1, mt.Labels("app", "front")),
		mt.Deployment("shop", "api", 1, mt.Labels("app", "api")),
		mt.Service("shop", "front", mt.Labels("app", "front")),
		mt.Ingress("shop", "web", "front"),
		exposed("shop", "api", corev1.ServiceTypeLoadBalancer),
	)
	got, err := json.Marshal(mustEntity(t, m, model.InternetID))
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"id":"ext/internet","kind":"Internet","group":"ext","name":"internet","namespace":"","labels":{},"status":"2 entrypoints","filterGroup":"ingress"}`
	if string(got) != want {
		t.Errorf("entity JSON\n got %s\nwant %s", got, want)
	}
	_, edges := m.Snapshot()
	found := false
	for _, e := range edges {
		if e.From != model.InternetID {
			continue
		}
		b, _ := json.Marshal(e)
		if e.To == "app/shop/Deployment/front" {
			found = true
			const w = `{"id":"routes:ext/internet-\u003eapp/shop/Deployment/front","from":"ext/internet","to":"app/shop/Deployment/front","kind":"routes"}`
			if string(b) != w {
				t.Errorf("edge JSON\n got %s\nwant %s", b, w)
			}
		}
	}
	if !found {
		t.Error("no routes edge ext/internet -> app/shop/Deployment/front")
	}
}
