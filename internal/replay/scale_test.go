package replay

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	mt "github.com/kesokaj/visual-kubernetes/internal/model/modeltest"
)

// scale is the deploy-scale recording's story (test/fixturegen): shop/web runs 2 pods behind
// a Service, the HPA scales it to 4, the ReplicaSet creates two pods, the scheduler binds
// them (binding and Scheduled Event in one batch), they start and turn ready, and the
// EndpointSlice lists them.
func scale() ([]model.Change, map[int64][]model.Change) {
	web := mt.Deployment("shop", "web", 2, mt.Labels("app", "web"))
	rs := mt.ReplicaSet(web, "7d4b9c")
	old := []*corev1.Pod{running(mt.PodOf(rs, "x000"), "node-1"), running(mt.PodOf(rs, "x001"), "node-2")}
	prime := ups(mt.Node("node-1", true), web, rs, old[0], old[1],
		mt.Service("shop", "web", mt.Labels("app", "web")), slice("shop", "web", old...),
		mt.HPA("shop", "web", "Deployment", "web", 2, 6, 2))
	scaled := managedBy(mt.Scaled(web, 4), "kube-controller-manager")
	rs4 := mt.ReplicaSet(scaled, "7d4b9c")
	fresh := []*corev1.Pod{mt.PodOf(rs4, "x002"), mt.PodOf(rs4, "x003")}
	all := append(slices.Clone(old), running(fresh[0], "node-2"), running(fresh[1], "node-3"))
	return prime, map[int64][]model.Change{
		1000: ups(mt.HPA("shop", "web", "Deployment", "web", 2, 6, 4), scaled, rs4,
			mt.Event(mt.Ref("apps/v1", "Deployment", "shop", "web"), corev1.EventTypeNormal, "ScalingReplicaSet", "up", at(1000))),
		1500: ups(fresh[0], fresh[1]),
		2500: ups(mt.Scheduled(fresh[0], "node-2"), mt.Scheduled(fresh[1], "node-3"),
			scheduledEvent(fresh[0], 2500), scheduledEvent(fresh[1], 2500)),
		4000: ups(all[2], all[3], slice("shop", "web", all...)),
	}
}

func TestScaleChainFollowsD17(t *testing.T) {
	prime, batches := scale()
	chains := latest(feed(t, prime, batches))
	if len(chains) != 1 {
		t.Fatalf("got %d chains, want 1: %+v", len(chains), chains)
	}
	c := chains[0]
	const dep, rs, slice = "apps/Deployment/shop/web", "apps/ReplicaSet/shop/web-7d4b9c", "discovery.k8s.io/EndpointSlice/shop/web-abcde"
	const p2, p3 = "core/Pod/shop/web-7d4b9c-x002", "core/Pod/shop/web-7d4b9c-x003"
	if c.ID != dep+"@2026-11-10T10:00:01.000Z" {
		t.Errorf("chain id %q", c.ID)
	}
	want := []Step{
		{"2026-11-10T10:00:01.000Z", "kube-controller-manager", VerbAuthentication, dep, true},
		{"2026-11-10T10:00:01.000Z", "kube-controller-manager", VerbAuthorization, dep, true},
		{"2026-11-10T10:00:01.000Z", "kube-controller-manager", VerbAdmission, dep, true},
		{"2026-11-10T10:00:01.000Z", "kube-controller-manager", VerbPersisted, dep, true},
		{"2026-11-10T10:00:01.000Z", "kube-controller-manager", VerbUpdated, dep, false},
		{"2026-11-10T10:00:01.000Z", "kube-controller-manager", VerbUpdated, rs, false},
		{"2026-11-10T10:00:01.500Z", "kube-controller-manager", VerbCreated, p2, false},
		{"2026-11-10T10:00:01.500Z", "kube-controller-manager", VerbCreated, p3, false},
		{"2026-11-10T10:00:02.500Z", "default-scheduler", VerbScheduled, p2, false},
		{"2026-11-10T10:00:02.500Z", "default-scheduler", VerbScheduled, p3, false},
		{"2026-11-10T10:00:04.000Z", "kubelet on node-2", VerbStarted, p2, false},
		{"2026-11-10T10:00:04.000Z", "kubelet on node-3", VerbStarted, p3, false},
		{"2026-11-10T10:00:04.000Z", "kubelet on node-2", VerbReady, p2, false},
		{"2026-11-10T10:00:04.000Z", "kubelet on node-3", VerbReady, p3, false},
		{"2026-11-10T10:00:04.000Z", "endpointslice-controller", VerbUpdated, slice, false},
	}
	if !slices.Equal(c.Steps, want) {
		t.Errorf("steps:\n%s\nwant:\n%s", dump(c.Steps), dump(want))
	}
	if err := d17Order(c.Steps); err != nil {
		t.Errorf("D17 order: %v", err)
	}
}

// TestD17OrderCheckFails holds the order check to its must-fail state: the scale chain with
// two steps swapped, a derived step dropped or moved after the observed ones is rejected.
func TestD17OrderCheckFails(t *testing.T) {
	prime, batches := scale()
	steps := latest(feed(t, prime, batches))[0].Steps
	mutations := map[string]func([]Step) []Step{
		"ready before started": func(s []Step) []Step { s[11], s[12] = s[12], s[11]; return s },
		"slice before ready":   func(s []Step) []Step { s[13], s[14] = s[14], s[13]; return s },
		"no admission":         func(s []Step) []Step { return slices.Delete(s, 2, 3) },
		"derived last":         func(s []Step) []Step { return append(s[1:], s[0]) },
		"pod before replicas":  func(s []Step) []Step { s[5], s[6] = s[6], s[5]; return s },
	}
	for name, mutate := range mutations {
		if err := d17Order(mutate(slices.Clone(steps))); err == nil {
			t.Errorf("%s: d17Order accepted the mutated chain", name)
		}
	}
}

// TestOneBatchKeepsTheOrder: a live watch batch can hold a whole rollout at once, in any
// order; the chain still reads root, children, pods.
func TestOneBatchKeepsTheOrder(t *testing.T) {
	prime, batches := scale()
	var all []model.Change
	for _, ms := range []int64{4000, 2500, 1500, 1000} { // newest first, the worst order
		all = append(all, batches[ms]...)
	}
	chains := latest(feed(t, prime, map[int64][]model.Change{1000: all}))
	if len(chains) != 1 {
		t.Fatalf("got %d chains, want 1", len(chains))
	}
	if err := d17Order(chains[0].Steps); err != nil {
		t.Errorf("D17 order: %v\n%s", err, dump(chains[0].Steps))
	}
	// the newest copy of each pod wins: Running and ready already, so all four pod steps show
	if n := len(chains[0].Steps); n != 15 {
		t.Errorf("%d steps, want 15:\n%s", n, dump(chains[0].Steps))
	}
}

func TestChainWireShape(t *testing.T) {
	b := New()
	evs := b.Observe(at(0), ups(mt.Pod("demo", "solo", nil, nil))...)
	if len(evs) != 1 {
		t.Fatalf("%d events, want 1", len(evs))
	}
	raw, err := json.Marshal(evs[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	steps, _ := got["steps"].([]any)
	if got["id"] != "core/Pod/demo/solo@2026-11-10T10:00:00.000Z" || len(steps) != 5 {
		t.Fatalf("chain = %s", raw)
	}
	first, _ := steps[0].(map[string]any)
	for _, k := range []string{"at", "actor", "verb", "object", "derived"} {
		if _, ok := first[k]; !ok {
			t.Errorf("step has no %q: %s", k, raw)
		}
	}
	if len(first) != 5 {
		t.Errorf("step has %d keys, want 5: %s", len(first), raw)
	}
}

// TestSentChainsAreCopies: a chain already handed to the hub never changes under it.
func TestSentChainsAreCopies(t *testing.T) {
	b := New()
	p := mt.Pod("demo", "solo", nil, nil)
	first := b.Observe(at(0), ups(p)...)[0].Data.(Chain)
	n := len(first.Steps)
	b.Observe(at(1000), ups(running(p, "node-1"))...)
	if len(first.Steps) != n || first.Steps[n-1].Verb != VerbCreated {
		t.Errorf("the sent chain changed: %s", dump(first.Steps))
	}
}

func dump(steps []Step) string {
	var b strings.Builder
	for i, s := range steps {
		fmt.Fprintf(&b, "  %2d %s %s %s %s derived=%v\n", i, s.At, s.Actor, s.Verb, s.Object, s.Derived)
	}
	return b.String()
}

// TestScaleDownAfterScaleUpIsItsOwnChain: scaling back down inside chainTTL starts a second
// chain, and the pod deletions land in it, not in the scale-up chain the pods were created in
// (that would make the old chain the newest one, the one R replays).
func TestScaleDownAfterScaleUpIsItsOwnChain(t *testing.T) {
	prime, batches := scale()
	web := mt.Deployment("shop", "web", 2, mt.Labels("app", "web"))
	down := managedBy(mt.Scaled(web, 2), "kube-controller-manager")
	rs2 := mt.ReplicaSet(down, "7d4b9c")
	rs4 := mt.ReplicaSet(mt.Scaled(web, 4), "7d4b9c")
	batches[6000] = ups(down, rs2)
	batches[6500] = dels(mt.PodOf(rs4, "x002"), mt.PodOf(rs4, "x003"))
	sent := feed(t, prime, batches)
	chains := latest(sent)
	if len(chains) != 2 {
		t.Fatalf("got %d chains, want 2: %+v", len(chains), chains)
	}
	const downID = "apps/Deployment/shop/web@2026-11-10T10:00:06.000Z"
	if got := sent[len(sent)-1].ID; got != downID {
		t.Errorf("newest chain sent is %q, want the scale-down %q", got, downID)
	}
	for _, c := range chains {
		deleted := slices.ContainsFunc(c.Steps, func(s Step) bool { return s.Verb == VerbDeleted })
		if deleted != (c.ID == downID) {
			t.Errorf("chain %s has deletions = %v:\n%s", c.ID, deleted, dump(c.Steps))
		}
	}
}
