package model_test

import (
	"errors"
	"testing"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	mt "github.com/kesokaj/visual-kubernetes/internal/model/modeltest"
)

func TestScaleProducesOrderedDeltas(t *testing.T) {
	m := model.New()
	d := mt.Deployment("demo", "web", 1, mt.Labels("app", "web"))
	rs := mt.ReplicaSet(d, "h")
	apply(m, mt.Node("w1", true), d, rs)

	pod := mt.PodOf(rs, "a")
	got := m.Apply(model.Change{Obj: mt.Scheduled(pod, "w1")})
	want := []struct {
		op model.DeltaOp
		id string
	}{
		{model.OpUpsert, "core/Pod/demo/web-h-a"},
		{model.OpEdgeUpsert, "hosts:core/Node/_/w1->app/demo/Deployment/web"},
	}
	if len(got) != len(want) {
		t.Fatalf("deltas = %+v, want %d", got, len(want))
	}
	for i, w := range want {
		if got[i].Op != w.op || got[i].ID != w.id {
			t.Errorf("delta %d = %s %s, want %s %s", i, got[i].Op, got[i].ID, w.op, w.id)
		}
	}

	// A resourceVersion bump that changes nothing drawn yields no delta.
	same := mt.Scheduled(pod, "w1")
	same.ResourceVersion = "99"
	if d := m.Apply(model.Change{Obj: same}); len(d) != 0 {
		t.Errorf("no-op update deltas = %+v, want none", d)
	}

	// Ready flips the app status; delete removes the pod, then the edge, then nothing else.
	got = m.Apply(model.Change{Obj: mt.Running(same, true)})
	if len(got) != 2 || got[0].ID != "app/demo/Deployment/web" || got[0].Entity.Status != "1/1 ready" || got[1].ID != "core/Pod/demo/web-h-a" {
		t.Errorf("ready deltas = %+v, want app status then pod upsert", got)
	}
	got = m.Apply(model.Change{Obj: same, Deleted: true})
	ops := []model.DeltaOp{model.OpUpsert, model.OpEdgeDelete, model.OpDelete}
	if len(got) != len(ops) {
		t.Fatalf("delete deltas = %+v, want app upsert, edge delete, pod delete", got)
	}
	for i, op := range ops {
		if got[i].Op != op {
			t.Errorf("delete delta %d = %s %s, want %s", i, got[i].Op, got[i].ID, op)
		}
	}
	if _, ok := m.Object("core/Pod/demo/web-h-a"); ok {
		t.Error("deleted pod still stored")
	}
}

func TestDeltaEventsWireShape(t *testing.T) {
	e := model.Entity{ID: "x"}
	edge := model.Edge{ID: "y"}
	evs := model.DeltaEvents([]model.Delta{
		{Op: model.OpUpsert, Entity: &e, ID: "x"},
		{Op: model.OpEdgeUpsert, Edge: &edge, ID: "y"},
		{Op: model.OpEdgeDelete, ID: "y"},
		{Op: model.OpDelete, ID: "x"},
	})
	types := []string{"upsert", "edgeUpsert", "edgeDelete", "delete"}
	for i, ev := range evs {
		if ev.Type != types[i] {
			t.Errorf("event %d type = %s, want %s", i, ev.Type, types[i])
		}
	}
	if _, ok := evs[0].Data.(model.EntityData); !ok {
		t.Errorf("upsert data = %T, want EntityData", evs[0].Data)
	}
	if d, ok := evs[3].Data.(model.IDData); !ok || d.ID != "x" {
		t.Errorf("delete data = %#v, want IDData{x}", evs[3].Data)
	}
}

func TestIDs(t *testing.T) {
	if got := model.ID("", "Node", "", "kind-worker"); got != "core/Node/_/kind-worker" {
		t.Errorf("node id = %s", got)
	}
	if got := model.ID("apps", "Deployment", "demo", "web"); got != "apps/Deployment/demo/web" {
		t.Errorf("deployment id = %s", got)
	}
	r, err := model.ParseID("core/Node/_/kind-worker")
	if err != nil || r != (model.Ref{Group: "core", Kind: "Node", Name: "kind-worker"}) {
		t.Errorf("ParseID node = %+v, %v", r, err)
	}
	if r.ID() != "core/Node/_/kind-worker" {
		t.Errorf("round trip = %s", r.ID())
	}
	for _, bad := range []string{"", "app/demo/Deployment/web", "cp/scheduler", "a/b/c/d/e", "core//demo/x", "core/Pod/demo/a b", "core/Pod/demo/\x1b[2J"} {
		if _, err := model.ParseID(bad); !errors.Is(err, model.ErrMalformedID) {
			t.Errorf("ParseID(%q) err = %v, want ErrMalformedID", bad, err)
		}
	}
}
