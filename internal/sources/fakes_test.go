package sources

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kesokaj/visual-kubernetes/internal/kubelet"
	"github.com/kesokaj/visual-kubernetes/internal/model"
	"github.com/kesokaj/visual-kubernetes/internal/model/modeltest"
	"github.com/kesokaj/visual-kubernetes/internal/stream"
)

var t0 = time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)

// response is one canned Raw answer.
type response struct {
	body []byte
	err  error
}

// fakeRaw answers by path and records the query of each request.
type fakeRaw struct {
	mu      sync.Mutex
	answers map[string]response
	queries map[string]url.Values
}

func (f *fakeRaw) Get(_ context.Context, path string, params url.Values) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.queries == nil {
		f.queries = map[string]url.Values{}
	}
	f.queries[path] = params
	a, ok := f.answers[path]
	if !ok {
		return nil, apierrors.NewNotFound(schema.GroupResource{}, path)
	}
	return a.body, a.err
}

// fakeKubelet serves one summary per host; summaries can be swapped between cycles.
type fakeKubelet struct {
	mu    sync.Mutex
	byIP  map[string]*kubelet.Summary
	err   error
	calls []string
}

func (f *fakeKubelet) Summary(_ context.Context, host string, port int) (*kubelet.Summary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, host)
	if f.err != nil {
		return nil, f.err
	}
	s, ok := f.byIP[host]
	if !ok {
		return nil, &kubelet.StatusError{Code: 404}
	}
	return s, nil
}

// loadSummary reads the recorded fixture.
func loadSummary(t *testing.T) *kubelet.Summary {
	t.Helper()
	b, err := os.ReadFile("testdata/summary.json")
	if err != nil {
		t.Fatal(err)
	}
	var s kubelet.Summary
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return &s
}

// advanced returns a copy of s whose pod counters grew by add bytes per second over d.
func advanced(s *kubelet.Summary, d time.Duration, addPerSec uint64) *kubelet.Summary {
	out := &kubelet.Summary{Node: s.Node}
	for _, p := range s.Pods {
		c := p
		if p.Network != nil {
			n := *p.Network
			rx, tx := *n.RxBytes+addPerSec*uint64(d.Seconds()), *n.TxBytes+addPerSec*uint64(d.Seconds())
			n.RxBytes, n.TxBytes, n.Time = &rx, &tx, n.Time.Add(d)
			c.Network = &n
		}
		out.Pods = append(out.Pods, c)
	}
	return out
}

// cluster is the model behind the fixture: node-1 with two web pods and a hostNetwork
// kube-proxy pod.
func cluster(t *testing.T, extra ...runtime.Object) *model.Model {
	t.Helper()
	node := modeltest.Node("node-1", true)
	node.Status.Addresses = []corev1.NodeAddress{{Type: corev1.NodeHostName, Address: "node-1"}, {Type: corev1.NodeInternalIP, Address: "10.0.0.1"}}
	node.Status.DaemonEndpoints.KubeletEndpoint.Port = 10250
	d := modeltest.Deployment("shop", "web", 2, modeltest.Labels("app", "web"))
	rs := modeltest.ReplicaSet(d, "7d4b9c")
	ds := modeltest.DaemonSet("kube-system", "kube-proxy", modeltest.Labels("k8s-app", "kube-proxy"), 1)
	kp := modeltest.Pod("kube-system", "kube-proxy-n1", modeltest.Labels("k8s-app", "kube-proxy"), modeltest.ControllerRef("apps/v1", "DaemonSet", "kube-proxy"))
	kp.Spec.HostNetwork = true
	objs := []runtime.Object{node, d, rs, ds,
		modeltest.Running(modeltest.Scheduled(modeltest.PodOf(rs, "a"), "node-1"), true),
		modeltest.Running(modeltest.Scheduled(modeltest.PodOf(rs, "b"), "node-1"), true),
		modeltest.Running(modeltest.Scheduled(kp, "node-1"), true),
	}
	m := model.New()
	var changes []model.Change
	for _, o := range append(objs, extra...) {
		changes = append(changes, model.Change{Obj: o})
	}
	m.Apply(changes...)
	return m
}

// appOf returns the AppID the model gave a pod.
func appOf(t *testing.T, m *model.Model, podID string) string {
	t.Helper()
	ents, _ := m.Snapshot()
	for _, e := range ents {
		if e.ID == podID {
			if e.AppID == "" {
				t.Fatalf("pod %s has no app", podID)
			}
			return e.AppID
		}
	}
	t.Fatalf("pod %s not in the model", podID)
	return ""
}

const readyzOK = "[+]ping ok\n[+]log ok\n[+]etcd ok\n[+]informer-sync ok\nreadyz check passed\n"

const metricsBody = `# HELP apiserver_resource_objects [ALPHA] Number of stored objects
# TYPE apiserver_resource_objects gauge
apiserver_resource_objects{group="",resource="pods"} 12
apiserver_resource_objects{group="apps",resource="deployments"} 3
apiserver_resource_objects{group="",resource="events"} -1
apiserver_storage_objects{resource="pods"} 999
`

const podMetricsBody = `{"kind":"PodMetricsList","apiVersion":"metrics.k8s.io/v1beta1","items":[
{"metadata":{"name":"web-7d4b9c-a","namespace":"shop"},"timestamp":"2026-11-10T09:00:00Z","window":"10s",
 "containers":[{"name":"app","usage":{"cpu":"12m","memory":"30Mi"}},{"name":"sidecar","usage":{"cpu":"1500u","memory":"1Mi"}}]},
{"metadata":{"name":"kube-proxy-n1","namespace":"kube-system"},"containers":[{"name":"kube-proxy","usage":{"cpu":"2m","memory":"18Mi"}}]}]}`

func healthyRaw() *fakeRaw {
	return &fakeRaw{answers: map[string]response{
		"/readyz":      {body: []byte(readyzOK)},
		"/metrics":     {body: []byte(metricsBody)},
		podMetricsPath: {body: []byte(podMetricsBody)},
	}}
}

// metricsOf returns the cycle's metrics data.
func metricsOf(t *testing.T, events []model.Event) stream.MetricsData {
	t.Helper()
	if len(events) == 0 || events[len(events)-1].Type != stream.EventMetrics {
		t.Fatalf("cycle did not end with a metrics event: %+v", events)
	}
	return events[len(events)-1].Data.(stream.MetricsData)
}

func sampleOf(md stream.MetricsData, id string) (stream.Sample, bool) {
	for _, s := range md.Samples {
		if s.ID == id {
			return s, true
		}
	}
	return stream.Sample{}, false
}

func lease(name, holder string, renew time.Time, seconds int32) *coordinationv1.Lease {
	mt := metav1.NewMicroTime(renew)
	return &coordinationv1.Lease{
		TypeMeta:   metav1.TypeMeta{APIVersion: "coordination.k8s.io/v1", Kind: "Lease"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "kube-system"},
		Spec:       coordinationv1.LeaseSpec{HolderIdentity: &holder, RenewTime: &mt, LeaseDurationSeconds: &seconds},
	}
}

var errBoom = errors.New("boom")
