package sources

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"time"

	"github.com/kesokaj/visual-kubernetes/internal/kubelet"
	"github.com/kesokaj/visual-kubernetes/internal/model"
	"github.com/kesokaj/visual-kubernetes/internal/probe"
	"github.com/kesokaj/visual-kubernetes/internal/stream"
)

const (
	webA      = "core/Pod/shop/web-7d4b9c-a"
	webB      = "core/Pod/shop/web-7d4b9c-b"
	kubeProxy = "core/Pod/kube-system/kube-proxy-n1"
)

// twoCycles runs a collector over the fixture and its copy 10 s later with every pod's counters
// up by 1000 B/s, returning the second cycle's events.
func twoCycles(t *testing.T, m *model.Model, raw *fakeRaw) []model.Event {
	t.Helper()
	s := loadSummary(t)
	kl := &fakeKubelet{byIP: map[string]*kubelet.Summary{"10.0.0.1": s}}
	now := t0
	c := New(Deps{Model: m, Raw: raw, Kubelet: kl, Out: nil, Now: func() time.Time { return now }})
	c.Collect(t.Context())
	kl.byIP["10.0.0.1"] = advanced(s, 10*time.Second, 1000)
	now = t0.Add(10 * time.Second)
	return c.Collect(t.Context())
}

// TestHostNetworkPodsExcluded: kube-proxy's counters are the node's eth0 (9.1 GB in the
// fixture); they must not size anything, while pod-network pods get their rate.
func TestHostNetworkPodsExcluded(t *testing.T) {
	m := cluster(t)
	md := metricsOf(t, twoCycles(t, m, healthyRaw()))
	if md.SizeBy != stream.SizeByNetwork {
		t.Errorf("sizeBy = %s, want network", md.SizeBy)
	}
	for _, id := range []string{webA, webB} {
		s, ok := sampleOf(md, id)
		if !ok || s.RxBps == nil || *s.RxBps != 1000 || *s.TxBps != 1000 {
			t.Errorf("%s sample = %+v, want rx/tx 1000 B/s", id, s)
		}
	}
	if s, ok := sampleOf(md, kubeProxy); !ok || s.RxBps != nil || s.TxBps != nil || s.CPUMilli == nil {
		t.Errorf("kube-proxy sample = %+v, want CPU only, no throughput", s)
	}
	if s, _ := sampleOf(md, appOf(t, m, kubeProxy)); s.RxBps != nil {
		t.Errorf("kube-proxy app has throughput %v, want none", *s.RxBps)
	}
	web, ok := sampleOf(md, appOf(t, m, webA))
	if !ok || web.RxBps == nil || *web.RxBps != 2000 || web.CPUMilli == nil || *web.CPUMilli != 14 {
		t.Errorf("web app sample = %+v, want rx 2000 B/s (two pods) and cpu 14m", web)
	}
	if _, ok := sampleOf(md, "core/Pod/shop/ghost-1"); ok {
		t.Error("a pod the model does not hold got a sample")
	}
}

// TestHostNetworkGuardMustFail validates the test above: with the pod on the pod network its
// node-sized counters do show up, so the exclusion is what keeps them out.
func TestHostNetworkGuardMustFail(t *testing.T) {
	m := cluster(t)
	obj, _ := m.Object(kubeProxy)
	pod := obj.(*corev1.Pod).DeepCopy()
	pod.Spec.HostNetwork = false
	m.Apply(model.Change{Obj: pod})
	md := metricsOf(t, twoCycles(t, m, healthyRaw()))
	if s, ok := sampleOf(md, kubeProxy); !ok || s.RxBps == nil {
		t.Fatalf("a pod-network kube-proxy should get a rate, got %+v", s)
	}
}

func TestSizeFallback(t *testing.T) {
	notServed := &fakeRaw{answers: map[string]response{"/readyz": {body: []byte(readyzOK)}}}
	for _, tc := range []struct {
		name        string
		raw         *fakeRaw
		want        stream.SizeBy
		metricsCap  probe.State
		wantSamples int
	}{
		{"no kubelet, metrics-server present: cpu", healthyRaw(), stream.SizeByCPU, probe.OK, 4},
		{"no kubelet, no metrics-server: pods", notServed, stream.SizeByPods, probe.Missing, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := New(Deps{Model: cluster(t), Raw: tc.raw, KubeletErr: probe.ErrLocalMode, Now: func() time.Time { return t0 }})
			events := c.Collect(t.Context())
			md := metricsOf(t, events)
			if md.SizeBy != tc.want || len(md.Samples) != tc.wantSamples {
				t.Errorf("sizeBy %s with %d samples, want %s with %d", md.SizeBy, len(md.Samples), tc.want, tc.wantSamples)
			}
			if got := c.caps["kubelet"].State; got != string(probe.Unreachable) {
				t.Errorf("kubelet capability = %s, want unreachable in local mode", got)
			}
			if got := c.caps["metrics.k8s.io"].State; got != string(tc.metricsCap) {
				t.Errorf("metrics.k8s.io capability = %s, want %s", got, tc.metricsCap)
			}
		})
	}
}

func TestKubeletCapability(t *testing.T) {
	m := cluster(t)
	for _, tc := range []struct {
		name string
		kl   probe.SummaryFetcher
		kerr error
		want probe.State
	}{
		{"all nodes answer", &fakeKubelet{byIP: map[string]*kubelet.Summary{"10.0.0.1": loadSummary(t)}}, nil, probe.OK},
		{"forbidden", &fakeKubelet{err: &kubelet.StatusError{Code: 403}}, nil, probe.Denied},
		{"client not built", nil, errBoom, probe.Error},
	} {
		c := New(Deps{Model: m, Raw: healthyRaw(), Kubelet: tc.kl, KubeletErr: tc.kerr, Now: func() time.Time { return t0 }})
		c.Collect(t.Context())
		if got := c.caps["kubelet"]; got.State != string(tc.want) {
			t.Errorf("%s: kubelet capability %+v, want %s", tc.name, got, tc.want)
		}
	}
	empty := New(Deps{Model: model.New(), Raw: healthyRaw(), Kubelet: &fakeKubelet{}, Now: func() time.Time { return t0 }})
	empty.Collect(t.Context())
	if got := empty.caps["kubelet"].State; got != string(probe.Missing) {
		t.Errorf("no nodes: kubelet capability %s, want missing", got)
	}
}

func TestParsePodMetrics(t *testing.T) {
	u, err := parsePodMetrics([]byte(podMetricsBody))
	if err != nil {
		t.Fatal(err)
	}
	if got := u[webA]; got.cpuMilli != 14 || got.memBytes != 31*1024*1024 {
		t.Errorf("web-a usage = %+v, want 14m (12m + 1500u = 13.5m, rounded up once) and 31Mi", got)
	}
	if _, err := parsePodMetrics([]byte("{")); err == nil {
		t.Error("malformed list parsed")
	}
}
