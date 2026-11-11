package hubble

import (
	"testing"

	"github.com/cilium/cilium/api/v1/flow"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	mt "github.com/kesokaj/visual-kubernetes/internal/model/modeltest"
)

// worldModel is one app "front" in shop; exposed adds a LoadBalancer Service so ext/internet
// is drawn.
func worldModel(exposed bool) *model.Model {
	d := mt.Deployment("shop", "front", 1, mt.Labels("app", "front"))
	rs := mt.ReplicaSet(d, "h")
	objs := []runtime.Object{d, rs, mt.Running(mt.Scheduled(mt.PodOf(rs, "0"), "n1"), true), mt.Node("n1", true)}
	svc := mt.Service("shop", "front", mt.Labels("app", "front"))
	if exposed {
		svc.Spec.Type = corev1.ServiceTypeLoadBalancer
	}
	objs = append(objs, svc)
	m := model.New()
	cs := make([]model.Change, len(objs))
	for i, o := range objs {
		cs[i] = model.Change{Obj: o}
	}
	m.Apply(cs...)
	return m
}

func TestWorldEndpointResolvesToInternet(t *testing.T) {
	front := &flow.Endpoint{Namespace: "shop", PodName: "front-h-0"}
	cases := []struct {
		name string
		ep   *flow.Endpoint
	}{
		{"label world", &flow.Endpoint{Labels: []string{"reserved:world"}}},
		{"label world-ipv4", &flow.Endpoint{Labels: []string{"reserved:world-ipv4"}}},
		{"identity 2", &flow.Endpoint{Identity: 2}},
		{"identity 9", &flow.Endpoint{Identity: 9}},
		{"identity 10", &flow.Endpoint{Identity: 10}},
	}
	idx := newIndex(worldModel(true))
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			agg := NewAggregator()
			agg.Add(&flow.Flow{Verdict: flow.Verdict_FORWARDED, Source: c.ep, Destination: front}, idx)
			agg.Add(&flow.Flow{Verdict: flow.Verdict_FORWARDED, Source: front, Destination: c.ep, TrafficDirection: flow.TrafficDirection_EGRESS}, idx)
			got := agg.Flush()
			if len(got) != 2 || got[0].Src != "app/shop/Deployment/front" || got[0].Dst != model.InternetID ||
				got[1].Src != model.InternetID || got[1].Dst != "app/shop/Deployment/front" {
				t.Errorf("flows = %+v, want front<->%s both ways", got, model.InternetID)
			}
		})
	}
}

func TestWorldEndpointNotWorld(t *testing.T) {
	idx := newIndex(worldModel(true))
	for _, ep := range []*flow.Endpoint{
		{Labels: []string{"reserved:host"}, Identity: 1},
		{Identity: 6},                    // remote-node
		{Namespace: "shop", Identity: 2}, // a namespaced endpoint is never the world
		// Cilium's kube-apiserver identity also carries reserved:world (labels.go:108-111):
		// API-server traffic must not be drawn as internet traffic (review DoD finding 2).
		{Labels: []string{"reserved:kube-apiserver", "reserved:world"}, Identity: 7},
		{Labels: []string{"reserved:world", "reserved:kube-apiserver"}},
	} {
		if got := idx.resolve(ep, nil); got == model.InternetID {
			t.Errorf("resolve(%+v) = %s, want not internet", ep, got)
		}
	}
}

func TestWorldDroppedWhenInternetNotDrawn(t *testing.T) {
	idx := newIndex(worldModel(false))
	if got := idx.resolve(&flow.Endpoint{Labels: []string{"reserved:world"}}, nil); got != "" {
		t.Errorf("resolve(world) = %q without an entrypoint, want \"\"", got)
	}
}

func TestPromExternalSides(t *testing.T) {
	idx := newIndex(worldModel(true))
	for _, m := range []map[string]string{
		{"source": "reserved:world", "destination_workload_namespace": "shop", "destination_workload": "front"},
		{"source_workload": "world", "destination_workload_namespace": "shop", "destination_workload": "front"},
		{"client": "reserved:world-ipv6", "server_namespace": "shop", "server": "front"},
	} {
		src, dst, _ := promEndpoints(m, idx)
		if src != model.InternetID || dst != "app/shop/Deployment/front" {
			t.Errorf("promEndpoints(%v) = %q -> %q, want %s -> front", m, src, dst, model.InternetID)
		}
	}
	// Not external: a namespaced "world", or Istio's unknown peer (D11).
	for _, m := range []map[string]string{
		{"source_workload_namespace": "shop", "source_workload": "world", "destination_workload_namespace": "shop", "destination_workload": "front"},
		{"source_workload": "unknown", "destination_workload_namespace": "shop", "destination_workload": "front"},
	} {
		if src, _, _ := promEndpoints(m, idx); src == model.InternetID {
			t.Errorf("promEndpoints(%v) src = internet, want not external", m)
		}
	}
	if src, _, _ := promEndpoints(map[string]string{"source": "reserved:world", "destination_workload_namespace": "shop", "destination_workload": "front"}, newIndex(worldModel(false))); src != "" {
		t.Errorf("external src = %q without an entrypoint, want \"\"", src)
	}
}
