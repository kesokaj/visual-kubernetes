package sources

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	"github.com/kesokaj/visual-kubernetes/internal/probe"
	"github.com/kesokaj/visual-kubernetes/internal/stream"
)

func TestRateTable(t *testing.T) {
	rt := rateTable{window: 10 * time.Second, h: map[string]*history{}}
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	rt.observe("p", "u1", reading{t: at(0), rx: 0, tx: 0}, at(0))
	if _, _, ok := rt.rate("p"); ok {
		t.Fatal("rate from one reading")
	}
	rt.observe("p", "u1", reading{t: at(0), rx: 50, tx: 50}, at(5)) // kubelet's cached sample: same time
	if _, _, ok := rt.rate("p"); ok {
		t.Fatal("rate from a reading whose time did not advance")
	}
	for s := 5; s <= 20; s += 5 {
		rt.observe("p", "u1", reading{t: at(s), rx: uint64(100 * s), tx: uint64(10 * s)}, at(s))
	}
	if rx, tx, ok := rt.rate("p"); !ok || rx != 100 || tx != 10 {
		t.Errorf("steady rate = %v/%v %v, want 100/10", rx, tx, ok)
	}
	if n := len(rt.h["p"].rs); n != 3 {
		t.Errorf("window keeps %d readings, want 3 (10 s span plus the newest)", n)
	}
	rt.observe("p", "u1", reading{t: at(25), rx: 10, tx: 10}, at(25)) // container restart reset the counters
	if _, _, ok := rt.rate("p"); ok {
		t.Error("rate across a counter reset")
	}
	rt.observe("p", "u1", reading{t: at(30), rx: 510, tx: 10}, at(30))
	if rx, _, _ := rt.rate("p"); rx != 100 {
		t.Errorf("rate after reset = %v, want 100", rx)
	}
	rt.observe("p", "u2", reading{t: at(35), rx: 1e9, tx: 1e9}, at(35)) // same name, new pod
	if _, _, ok := rt.rate("p"); ok {
		t.Error("rate across a pod UID change")
	}
	rt.prune(at(35 + 30))
	if _, ok := rt.h["p"]; !ok {
		t.Error("pruned at exactly three windows")
	}
	rt.prune(at(35 + 31))
	if _, ok := rt.h["p"]; ok {
		t.Error("not pruned after three windows unseen")
	}
}

func TestParseReadyz(t *testing.T) {
	body := "[+]ping ok\n[-]etcd failed: reason withheld\n[+]poststarthook/start-informers ok\nnot a check\n[x]odd\nreadyz check failed\n"
	got := parseReadyz([]byte(body))
	want := map[string]bool{"ping": true, "etcd": false, "poststarthook/start-informers": true}
	if len(got) != len(want) {
		t.Fatalf("checks = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("check %s = %v, want %v", k, got[k], v)
		}
	}
}

func TestParseObjectCount(t *testing.T) {
	n, name, ok := parseObjectCount([]byte(metricsBody))
	if !ok || n != 15 || name != "apiserver_resource_objects" {
		t.Errorf("count = %d %s %v, want 15 from apiserver_resource_objects (negative skipped)", n, name, ok)
	}
	n, name, ok = parseObjectCount([]byte("apiserver_storage_objects{resource=\"pods\"} 7\napiserver_storage_objects_total 99\n"))
	if !ok || n != 7 || name != "apiserver_storage_objects" {
		t.Errorf("fallback count = %d %s %v, want 7 from apiserver_storage_objects", n, name, ok)
	}
	if _, _, ok := parseObjectCount([]byte("go_goroutines 12\n")); ok {
		t.Error("count found without a gauge")
	}
}

// cpByID collects the control-plane upserts of a cycle.
func cpByID(events []model.Event) map[string]model.Entity {
	out := map[string]model.Entity{}
	for _, e := range events {
		if d, ok := e.Data.(model.EntityData); ok && e.Type == string(model.OpUpsert) {
			out[d.Entity.ID] = d.Entity
		}
	}
	return out
}

func TestControlPlaneInference(t *testing.T) {
	fresh := lease("kube-scheduler", "kind-control-plane_4b1c2d3e-aaaa", t0.Add(-5*time.Second), 15)
	stale := lease("kube-controller-manager", "cp-0_9f8e", t0.Add(-time.Minute), 15)
	raw := healthyRaw()
	c := New(Deps{Model: cluster(t, fresh, stale), Raw: raw, KubeletErr: probe.ErrLocalMode, Now: func() time.Time { return t0 }})
	events := c.Collect(t.Context())
	cp := cpByID(events)
	for id, want := range map[string]string{
		model.APIServerID:   "ready · 15 objects",
		etcdID:              "ok (readyz etcd)",
		schedulerID:         "leader kind-control-plane",
		controllerManagerID: "lease expired (last leader cp-0)",
	} {
		if got := cp[id]; got.Status != want || got.FilterGroup != model.ControlPlane || got.Group != "cp" {
			t.Errorf("%s = %+v, want status %q in group cp/controlPlane", id, got, want)
		}
	}
	if ev := cp[schedulerID].Labels[EvidenceLabel]; ev != evidenceLease {
		t.Errorf("scheduler evidence = %q, want lease", ev)
	}
	if q := raw.queries["/readyz"]; q.Get("verbose") == "" {
		t.Errorf("readyz query = %v, want verbose", q)
	}
	edges := 0
	for _, e := range events {
		if e.Type == string(model.OpEdgeUpsert) {
			edges++
		}
	}
	if edges != 3 {
		t.Errorf("edge upserts = %d, want 3 (apiserver → etcd, scheduler, controller-manager)", edges)
	}
}

// TestControlPlaneManaged: a managed control plane hides the Leases and may fail /readyz
// partially; every part still shows, marked as inferred.
func TestControlPlaneManaged(t *testing.T) {
	raw := &fakeRaw{answers: map[string]response{
		"/readyz": {body: []byte("[+]ping ok\n[-]informer-sync failed\nreadyz check failed\n"), err: apierrors.NewInternalError(errBoom)},
	}}
	c := New(Deps{Model: cluster(t), Raw: raw, KubeletErr: probe.ErrLocalMode, Now: func() time.Time { return t0 }})
	cp := cpByID(c.Collect(t.Context()))
	want := map[string]string{
		model.APIServerID:   "not ready: informer-sync",
		etcdID:              "inferred: not reported by /readyz",
		schedulerID:         "inferred: no leader lease visible (managed control plane?)",
		controllerManagerID: "inferred: no leader lease visible (managed control plane?)",
	}
	for id, status := range want {
		if cp[id].Status != status {
			t.Errorf("%s status = %q, want %q", id, cp[id].Status, status)
		}
	}
	if c.caps["apiserver-metrics"].State != string(probe.Missing) {
		t.Errorf("apiserver-metrics = %+v, want missing", c.caps["apiserver-metrics"])
	}
	unreadable := New(Deps{Model: cluster(t), Raw: &fakeRaw{}, KubeletErr: probe.ErrLocalMode, Now: func() time.Time { return t0 }})
	if got := cpByID(unreadable.Collect(t.Context()))[model.APIServerID]; got.Labels[EvidenceLabel] != evidenceNone {
		t.Errorf("unreadable readyz: apiserver %+v, want evidence none", got)
	}
}

// TestEventsOnlyOnChange: a steady cluster sends only `metrics` after the first cycle.
func TestEventsOnlyOnChange(t *testing.T) {
	raw := healthyRaw()
	initial := map[string]model.Capability{"leases": {State: "ok", Detail: "3 leases"}}
	c := New(Deps{Model: cluster(t), Raw: raw, KubeletErr: probe.ErrLocalMode, Initial: initial, Now: func() time.Time { return t0 }})
	first := c.Collect(t.Context())
	var caps *stream.CapabilitiesData
	for _, e := range first {
		if d, ok := e.Data.(stream.CapabilitiesData); ok {
			caps = &d
		}
	}
	if caps == nil || caps.Capabilities["leases"].Detail != "3 leases" || caps.Capabilities["readyz"].State != "ok" {
		t.Fatalf("first cycle capabilities = %+v, want the probe's plus the polled sources", caps)
	}
	if second := c.Collect(t.Context()); len(second) != 1 || second[0].Type != stream.EventMetrics {
		t.Fatalf("steady cycle = %v, want only metrics", typesOf(second))
	}
	raw.answers["/readyz"] = response{body: []byte("[-]etcd failed\n"), err: apierrors.NewInternalError(errBoom)}
	third := c.Collect(t.Context())
	cp := cpByID(third)
	if len(cp) != 2 || cp[etcdID].Status != "not ready (readyz etcd failing)" {
		t.Errorf("after etcd fails: upserts %v, want apiserver and etcd", cp)
	}
}

func typesOf(events []model.Event) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Type
	}
	return out
}

// TestRESTRaw: the query goes out as parameters (not an escaped path), and a non-2xx answer
// still returns its body, which readyz parses when the API server is not ready.
func TestRESTRaw(t *testing.T) {
	var gotPath, gotQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("[-]etcd failed\nreadyz check failed\n"))
	}))
	defer ts.Close()
	cs, err := kubernetes.NewForConfig(&rest.Config{Host: ts.URL})
	if err != nil {
		t.Fatal(err)
	}
	body, err := RESTRaw{Client: cs.Discovery().RESTClient()}.Get(t.Context(), "/readyz", url.Values{"verbose": {"true"}})
	if err == nil || gotPath != "/readyz" || gotQuery != "verbose=true" {
		t.Fatalf("GET %s?%s err %v, want /readyz?verbose=true and an error", gotPath, gotQuery, err)
	}
	if checks := parseReadyz(body); len(checks) != 1 || checks["etcd"] {
		t.Errorf("body on 500 parsed to %v, want etcd failing", checks)
	}
}

func TestDetailSanitised(t *testing.T) {
	long := make([]byte, 500)
	for i := range long {
		long[i] = 'x'
	}
	if got := detail("a\x00b\n\tc  d" + string(long)); len(got) > maxDetail+len("…") || got[:7] != "ab c d"+"x" {
		t.Errorf("detail = %q", got[:20])
	}
}
