package model_test

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	mt "github.com/kesokaj/visual-kubernetes/internal/model/modeltest"
)

var t0 = time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)

// TestApplyEventsStreamsKubeEvents: an Event becomes one `event` message after the deltas of
// the same batch and is never stored, so it reaches neither the view nor Object (R2).
func TestApplyEventsStreamsKubeEvents(t *testing.T) {
	m := model.New()
	ev := mt.Event(mt.Ref("apps/v1", "Deployment", "shop", "web"), "Normal", "ScalingReplicaSet", "Scaled up replica set web-7d4b9c to 3", t0)
	out := m.ApplyEvents(model.Change{Obj: mt.Node("node-1", true)}, model.Change{Obj: ev})
	if len(out) < 2 {
		t.Fatalf("got %d events, want the node's deltas then one event", len(out))
	}
	for _, e := range out[:len(out)-1] {
		if e.Type == model.EventK8s {
			t.Errorf("an event came before the deltas: %+v", out)
		}
	}
	last := out[len(out)-1]
	want := model.K8sEventData{K8sEvent: model.K8sEvent{
		ID: "apps/Deployment/shop/web", Type: "Normal", Reason: "ScalingReplicaSet",
		Message: "Scaled up replica set web-7d4b9c to 3", At: "2026-11-10T10:00:00Z",
	}}
	if last.Type != model.EventK8s || last.Data != want {
		t.Errorf("last = %s %+v, want event %+v", last.Type, last.Data, want)
	}
	if _, ok := m.Object(model.ID("events.k8s.io", "Event", "shop", ev.Name)); ok {
		t.Error("the Event was stored")
	}
	ents, _ := m.Snapshot()
	for _, e := range ents {
		if e.Kind == "Event" {
			t.Errorf("an Event entered the view: %+v", e)
		}
	}
}

func TestApplyEventsSkips(t *testing.T) {
	ev := mt.Event(mt.Ref("v1", "Pod", "shop", "web-1"), "Warning", "BackOff", "Back-off", t0)
	cases := map[string]model.Change{
		"deleted Event":       {Obj: ev, Deleted: true},
		"Event about nothing": {Obj: mt.Event(corev1.ObjectReference{}, "Normal", "X", "y", t0)},
		"not an Event":        {Obj: mt.ConfigMap("shop", "c")},
		// security review 2026-11-10-1509, finding 1 (PRD R2)
		"Event about a Secret": {Obj: mt.Event(mt.Ref("v1", "Secret", "shop", "db-pass"), "Warning", "Failed", "bad key", t0)},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			for _, e := range model.New().ApplyEvents(c) {
				if e.Type == model.EventK8s {
					t.Errorf("got %+v, want no event", e)
				}
			}
		})
	}
}

// TestKubeEventCoreV1: a core/v1 Event about a cluster-scoped Node gets the `_` namespace
// segment, and its newest time wins (a repeating Event's series or lastTimestamp).
func TestKubeEventCoreV1(t *testing.T) {
	cases := []struct {
		name string
		ev   *corev1.Event
		at   string
	}{
		{"lastTimestamp after firstTimestamp", &corev1.Event{
			FirstTimestamp: metav1.NewTime(t0), LastTimestamp: metav1.NewTime(t0.Add(time.Minute)),
		}, "2026-11-10T10:01:00Z"},
		{"series after eventTime", &corev1.Event{
			EventTime: metav1.NewMicroTime(t0), Series: &corev1.EventSeries{LastObservedTime: metav1.NewMicroTime(t0.Add(2 * time.Minute))},
		}, "2026-11-10T10:02:00Z"},
		{"creationTimestamp only", &corev1.Event{ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(t0)}}, "2026-11-10T10:00:00Z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.ev.InvolvedObject = mt.Ref("v1", "Node", "", "node-2")
			tc.ev.Type, tc.ev.Reason, tc.ev.Message = "Normal", "NodeReady", "Node node-2 status is now: NodeReady"
			got, ok := model.KubeEvent(tc.ev)
			want := model.K8sEvent{ID: "core/Node/_/node-2", Type: "Normal", Reason: "NodeReady", Message: tc.ev.Message, At: tc.at}
			if !ok || got != want {
				t.Errorf("KubeEvent = %+v, %v; want %+v", got, ok, want)
			}
		})
	}
}
