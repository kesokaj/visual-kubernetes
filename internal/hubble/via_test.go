package hubble

import (
	"testing"

	"github.com/cilium/cilium/api/v1/flow"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	mt "github.com/kesokaj/visual-kubernetes/internal/model/modeltest"
)

const (
	viaWeb    = "app/demo/Deployment/web"
	viaAPI    = "app/demo/Deployment/api"
	viaAPISvc = "core/Service/demo/api"
)

// viaModel has apps web and api (tier=back) in demo plus the given extra objects.
func viaModel(extra ...runtime.Object) *model.Model {
	var objs []runtime.Object
	for _, app := range []string{"web", "api"} {
		d := mt.Deployment("demo", app, 1, mt.Labels("app", app, "tier", map[string]string{"web": "front", "api": "back"}[app]))
		rs := mt.ReplicaSet(d, "h")
		objs = append(objs, d, rs, mt.Running(mt.Scheduled(mt.PodOf(rs, "0"), "n1"), true))
	}
	objs = append(objs, mt.Node("n1", true))
	objs = append(objs, extra...)
	m := model.New()
	cs := make([]model.Change, len(objs))
	for i, o := range objs {
		cs[i] = model.Change{Obj: o}
	}
	m.Apply(cs...)
	return m
}

func svcPorts(s *corev1.Service, ports ...corev1.ServicePort) *corev1.Service {
	s.Spec.Ports = ports
	return s
}

func webToAPI(svc *flow.Service, port uint32, dir flow.TrafficDirection) *flow.Flow {
	f := &flow.Flow{
		Verdict:            flow.Verdict_FORWARDED,
		Source:             &flow.Endpoint{Namespace: "demo", PodName: "web-h-0"},
		Destination:        &flow.Endpoint{Namespace: "demo", PodName: "api-h-0"},
		DestinationService: svc,
		TrafficDirection:   dir,
	}
	if port != 0 {
		f.L4 = &flow.Layer4{Protocol: &flow.Layer4_TCP{TCP: &flow.TCP{DestinationPort: port}}}
	}
	return f
}

func flushOne(t *testing.T, idx *modelIndex, fs ...*flow.Flow) []Flow {
	t.Helper()
	agg := NewAggregator()
	for _, f := range fs {
		agg.Add(f, idx)
	}
	return agg.Flush()
}

func TestAggregatorRoutesViaDestinationService(t *testing.T) {
	// "edge" selects api and worker (EdgeSelects); destination_service names it, so it wins over
	// the app-owned "api" Service.
	idx := newIndex(viaModel(mt.Service("demo", "api", mt.Labels("app", "api")), mt.Service("demo", "edge", mt.Labels("tier", "back")),
		mt.Deployment("demo", "worker", 1, mt.Labels("app", "worker", "tier", "back"))))
	got := flushOne(t, idx, webToAPI(&flow.Service{Namespace: "demo", Name: "edge"}, 0, flow.TrafficDirection_EGRESS))
	if len(got) != 1 || got[0].Via != "core/Service/demo/edge" {
		t.Fatalf("flows = %+v, want one flow via core/Service/demo/edge", got)
	}
}

func TestAggregatorNoViaWithoutService(t *testing.T) {
	cases := map[string]*model.Model{
		"no service":           viaModel(),
		"service selects none": viaModel(mt.Service("demo", "api", mt.Labels("app", "nothing"))),
		"service of other app": viaModel(mt.Service("demo", "web", mt.Labels("app", "web"))),
		"service, no selector": viaModel(mt.Service("demo", "api", nil)),
	}
	for name, m := range cases {
		t.Run(name, func(t *testing.T) {
			got := flushOne(t, newIndex(m), webToAPI(&flow.Service{Namespace: "demo", Name: "api"}, 80, flow.TrafficDirection_EGRESS))
			if len(got) != 1 || got[0].Dst != viaAPI || got[0].Via != "" {
				t.Fatalf("flows = %+v, want web->api without via", got)
			}
		})
	}
}

func TestViaInferredFromUniqueService(t *testing.T) {
	// kube-proxy clusters: destination_service is null, the model still knows api's one Service.
	idx := newIndex(viaModel(mt.Service("demo", "api", mt.Labels("app", "api"))))
	got := flushOne(t, idx, webToAPI(nil, 0, flow.TrafficDirection_EGRESS))
	if len(got) != 1 || got[0].Src != viaWeb || got[0].Dst != viaAPI || got[0].Via != viaAPISvc {
		t.Fatalf("flows = %+v, want web->api via %s", got, viaAPISvc)
	}
}

func TestViaPortDisambiguates(t *testing.T) {
	httpSvc := svcPorts(mt.Service("demo", "api", mt.Labels("app", "api")), corev1.ServicePort{Port: 80, TargetPort: intstr.FromInt32(8080)})
	metrics := svcPorts(mt.Service("demo", "api-metrics", mt.Labels("app", "api")), corev1.ServicePort{Port: 9090})
	named := svcPorts(mt.Service("demo", "api-named", mt.Labels("app", "api")), corev1.ServicePort{Port: 8080, TargetPort: intstr.FromString("http")})
	idx := newIndex(viaModel(httpSvc, metrics, named))
	cases := []struct {
		port uint32
		want string
	}{
		{8080, viaAPISvc},                       // targetPort 8080; the named port never matches
		{9090, "core/Service/demo/api-metrics"}, // targetPort unset -> port
		{80, ""},                                // a Service port is not a pod port
		{0, ""},                                 // no L4 port, several candidates
	}
	for _, c := range cases {
		got := flushOne(t, idx, webToAPI(nil, c.port, flow.TrafficDirection_EGRESS))
		if len(got) != 1 || got[0].Via != c.want {
			t.Errorf("port %d: flows = %+v, want via %q", c.port, got, c.want)
		}
	}
}

func TestViaSkipsUndrawnOrSelf(t *testing.T) {
	idx := newIndex(viaModel(mt.Service("demo", "api", mt.Labels("app", "api"))))
	if got := idx.via(webToAPI(nil, 0, 0), viaWeb, viaAPISvc); got != "" {
		t.Errorf("via with dst == Service = %q, want empty", got)
	}
	if got := idx.via(webToAPI(nil, 0, 0), viaAPISvc, viaAPI); got != "" {
		t.Errorf("via with src == Service = %q, want empty", got)
	}
	if got := idx.via(webToAPI(nil, 0, 0), viaWeb, model.InternetID); got != "" {
		t.Errorf("via to the internet = %q, want empty", got)
	}
	// destination_service naming a Service the model does not draw falls back to inference.
	if got := idx.via(webToAPI(&flow.Service{Namespace: "demo", Name: "ghost"}, 0, 0), viaWeb, viaAPI); got != viaAPISvc {
		t.Errorf("via with unknown destination_service = %q, want %s", got, viaAPISvc)
	}
	idx.services[viaAPI] = nil // as when the Service is folded or not drawn
	if got := idx.via(webToAPI(&flow.Service{Namespace: "demo", Name: "api"}, 0, 0), viaWeb, viaAPI); got != "" {
		t.Errorf("via with no drawn Service = %q, want empty", got)
	}
}

func TestAggregatorMixedDirectionsKeepVia(t *testing.T) {
	// One connection: the EGRESS trace names the Service, the INGRESS trace does not. They must
	// stay one FORWARDED flow (cold read finding 1), carrying the via. Two Services and no port
	// keep inference out, so only destination_service yields a via.
	idx := newIndex(viaModel(mt.Service("demo", "api", mt.Labels("app", "api")), mt.Service("demo", "api2", mt.Labels("app", "api"))))
	got := flushOne(t, idx,
		webToAPI(&flow.Service{Namespace: "demo", Name: "api"}, 0, flow.TrafficDirection_EGRESS),
		webToAPI(nil, 0, flow.TrafficDirection_INGRESS))
	if len(got) != 1 || got[0].Verdict != VerdictForwarded || got[0].Count != 1 || got[0].Via != viaAPISvc {
		t.Fatalf("flows = %+v, want one FORWARDED count 1 via %s", got, viaAPISvc)
	}
}

func TestAggregatorDroppedGetsVia(t *testing.T) {
	idx := newIndex(viaModel(mt.Service("demo", "api", mt.Labels("app", "api"))))
	drop := webToAPI(nil, 0, flow.TrafficDirection_INGRESS)
	drop.Verdict = flow.Verdict_DROPPED
	got := flushOne(t, idx, drop, webToAPI(nil, 0, flow.TrafficDirection_EGRESS))
	if len(got) != 1 || got[0].Verdict != VerdictDropped || got[0].Via != viaAPISvc {
		t.Fatalf("flows = %+v, want one DROPPED via %s", got, viaAPISvc)
	}
}
