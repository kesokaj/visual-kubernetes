package replay

import (
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	mt "github.com/kesokaj/visual-kubernetes/internal/model/modeltest"
)

func verbs(steps []Step) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.Verb + " " + s.Object
	}
	return out
}

func TestNewDeploymentStartsACreatedChain(t *testing.T) {
	d := mt.Deployment("demo", "api", 1, mt.Labels("app", "api"))
	rs := mt.ReplicaSet(d, "5f6")
	p := mt.PodOf(rs, "a")
	chains := latest(feed(t, nil, map[int64][]model.Change{
		0:    ups(d),
		500:  ups(rs),
		1000: ups(p),
		2000: ups(running(p, "node-1")),
	}))
	if len(chains) != 1 {
		t.Fatalf("got %d chains, want 1", len(chains))
	}
	got := verbs(chains[0].Steps[4:])
	want := []string{
		"created apps/Deployment/demo/api", "created apps/ReplicaSet/demo/api-5f6",
		"created core/Pod/demo/api-5f6-a", "scheduled core/Pod/demo/api-5f6-a",
		"started core/Pod/demo/api-5f6-a", "ready core/Pod/demo/api-5f6-a",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("steps %v\nwant %v", got, want)
	}
	if a := chains[0].Steps[0].Actor; a != actorClient {
		t.Errorf("derived actor %q without managedFields, want %q", a, actorClient)
	}
}

func TestDeleteStartsADeletedChain(t *testing.T) {
	d := mt.Deployment("demo", "api", 1, mt.Labels("app", "api"))
	rs := mt.ReplicaSet(d, "5f6")
	p := running(mt.PodOf(rs, "a"), "node-1")
	chains := latest(feed(t, ups(d, rs, p), map[int64][]model.Change{
		0:   dels(d),
		100: dels(rs),
		200: dels(p),
	}))
	if len(chains) != 1 {
		t.Fatalf("got %d chains, want 1", len(chains))
	}
	got := verbs(chains[0].Steps[4:])
	want := []string{"deleted apps/Deployment/demo/api", "deleted apps/ReplicaSet/demo/api-5f6", "deleted core/Pod/demo/api-5f6-a"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("steps %v\nwant %v", got, want)
	}
}

// TestStatusOnlyChangesAreNoSteps: status updates (ready counts, conditions) and unrelated
// kinds start nothing, and pods outside every chain add nothing.
func TestStatusOnlyChangesAreNoSteps(t *testing.T) {
	d := mt.Deployment("demo", "api", 2, mt.Labels("app", "api"))
	rs := mt.ReplicaSet(d, "5f6")
	p := mt.PodOf(rs, "a")
	moved := d.DeepCopy()
	moved.Status.ReadyReplicas = 2
	moved.Labels = mt.Labels("app", "api", "tier", "x")
	b := New()
	b.Prime(ups(d, rs, p)...)
	for i, batch := range [][]model.Change{
		ups(moved),
		ups(running(p, "node-1")),
		ups(mt.ConfigMap("demo", "cfg"), mt.Service("demo", "api", nil), mt.Node("node-9", true)),
		ups(scheduledEvent(p, 0)),
	} {
		if evs := b.Observe(at(int64(i)), batch...); len(evs) != 0 {
			t.Errorf("batch %d made %d chain events, want 0", i, len(evs))
		}
	}
}

func TestScheduledEventBeforeBindingIsOneStep(t *testing.T) {
	p := mt.Pod("demo", "solo", nil, nil)
	chains := latest(feed(t, nil, map[int64][]model.Change{
		0:    ups(p),
		1000: ups(scheduledEvent(p, 1000)),
		1500: ups(mt.Scheduled(p, "node-1")),
		2000: ups(running(p, "node-1")),
	}))
	got := verbs(chains[0].Steps[4:])
	want := []string{"created core/Pod/demo/solo", "scheduled core/Pod/demo/solo", "started core/Pod/demo/solo", "ready core/Pod/demo/solo"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("steps %v\nwant %v", got, want)
	}
}

func TestFailedSchedulingIsAStep(t *testing.T) {
	p := mt.Pod("demo", "big", nil, nil)
	failed := mt.Event(mt.Ref("v1", "Pod", "demo", "big"), corev1.EventTypeWarning, "FailedScheduling", "0/3 nodes", at(500))
	chains := latest(feed(t, nil, map[int64][]model.Change{
		0:    ups(p),
		500:  ups(mt.Unschedulable(p), failed),
		900:  ups(failed),
		5000: ups(mt.Scheduled(p, "node-4")),
	}))
	got := verbs(chains[0].Steps[4:])
	want := []string{"created core/Pod/demo/big", "failed scheduling core/Pod/demo/big", "scheduled core/Pod/demo/big"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("steps %v\nwant %v", got, want)
	}
}

// TestOpenChainsAreBounded: past maxChains the oldest chain closes, so its pods' later steps
// are dropped while the newest chains keep growing.
func TestOpenChainsAreBounded(t *testing.T) {
	b := New()
	var pods []*corev1.Pod
	for i := range maxChains + 4 {
		p := mt.Pod("demo", fmt.Sprintf("p%02d", i), nil, nil)
		pods = append(pods, p)
		b.Observe(at(int64(i)), ups(p)...)
	}
	if len(b.chains) != maxChains || len(b.order) != maxChains {
		t.Fatalf("%d open chains (%d ordered), want %d", len(b.chains), len(b.order), maxChains)
	}
	if evs := b.Observe(at(100), ups(running(pods[0], "node-1"))...); len(evs) != 0 {
		t.Errorf("a closed chain gained steps: %+v", evs)
	}
	if evs := b.Observe(at(101), ups(running(pods[len(pods)-1], "node-1"))...); len(evs) != 1 {
		t.Errorf("the newest chain made %d events, want 1", len(evs))
	}
	for obj, id := range b.of {
		if _, ok := b.chains[id]; !ok {
			t.Errorf("%s maps to closed chain %s", obj, id)
		}
	}
}

func TestStepsAreBounded(t *testing.T) {
	d := mt.Deployment("demo", "wide", 100, mt.Labels("app", "wide"))
	rs := mt.ReplicaSet(d, "1")
	var pods []model.Change
	for i := range 100 {
		pods = append(pods, ups(running(mt.PodOf(rs, fmt.Sprint(i)), "node-1"))...)
	}
	chains := latest(feed(t, nil, map[int64][]model.Change{0: ups(d, rs), 1: pods}))
	if n := len(chains[0].Steps); n != MaxSteps {
		t.Errorf("%d steps, want the cap %d", n, MaxSteps)
	}
}

func TestChainsCloseAfterTTL(t *testing.T) {
	p := mt.Pod("demo", "slow", nil, nil)
	b := New()
	b.Observe(at(0), ups(p)...)
	late := at(0).Add(chainTTL + time.Second)
	if evs := b.Observe(late, ups(running(p, "node-1"))...); len(evs) != 0 {
		t.Errorf("an expired chain gained steps: %+v", evs)
	}
	if len(b.chains) != 0 || len(b.of) != 0 {
		t.Errorf("expired chain still held: %d chains, %d mappings", len(b.chains), len(b.of))
	}
}

func TestNewestManagerIsTheActor(t *testing.T) {
	d := mt.Deployment("demo", "api", 1, mt.Labels("app", "api"))
	if got := manager(managedBy(d, "hpa"), actorClient); got != "hpa" {
		t.Errorf("manager = %q, want hpa (the newest entry)", got)
	}
	if got := manager(d, actorClient); got != actorClient {
		t.Errorf("manager without managedFields = %q, want the fallback", got)
	}
}

// TestPrimeKeepsOnlyChainKinds: the initial list's Nodes, Services and ConfigMaps are never
// observed, so Prime does not keep them (they would outlive their objects on a live server).
func TestPrimeKeepsOnlyChainKinds(t *testing.T) {
	d := mt.Deployment("demo", "api", 1, mt.Labels("app", "api"))
	b := New()
	b.Prime(ups(mt.Node("node-1", true), mt.Service("demo", "api", mt.Labels("app", "api")), mt.ConfigMap("demo", "cfg"), d)...)
	if len(b.objs) != 1 {
		t.Errorf("Prime kept %d objects, want only the Deployment: %v", len(b.objs), b.objs)
	}
}
