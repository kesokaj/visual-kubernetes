package stream

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

// clock is a settable time source for the hub.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func entity(id, status string) model.Entity {
	return model.Entity{ID: id, Kind: "Pod", Group: "core", Name: id, Status: status, FilterGroup: model.Pods}
}

func upsert(id, status string) model.Event {
	return model.Event{Type: string(model.OpUpsert), Data: model.EntityData{Entity: entity(id, status)}}
}

func snapshotOf(ents ...model.Entity) model.Event {
	return model.Event{Type: EventSnapshot, Data: model.SnapshotData{Entities: ents, Edges: []model.Edge{}, Capabilities: map[string]model.Capability{}}}
}

func newTestHub(t *testing.T, o HubOptions) (*Hub, *clock) {
	t.Helper()
	c := &clock{t: time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)}
	o.Now = c.now
	h := NewHub(o)
	if err := h.Publish(snapshotOf(entity("core/Pod/ns/a", "Running"))); err != nil {
		t.Fatal(err)
	}
	return h, c
}

func publish(t *testing.T, h *Hub, events ...model.Event) {
	t.Helper()
	if err := h.Publish(events...); err != nil {
		t.Fatal(err)
	}
}

func types(es []Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Type
	}
	return out
}

func TestSubscribeResume(t *testing.T) {
	h, _ := newTestHub(t, HubOptions{})
	first := h.last // the snapshot's id
	publish(t, h, upsert("core/Pod/ns/b", "Pending"), upsert("core/Pod/ns/b", "Running"))
	for _, tc := range []struct {
		name, lastID string
		want         []string
		wantFirstID  uint64
	}{
		{"no id gets a snapshot", "", []string{EventSnapshot}, first + 2},
		{"malformed id gets a snapshot", "abc", []string{EventSnapshot}, first + 2},
		{"recent id gets only the missed events", strconv.FormatUint(first+1, 10), []string{"upsert"}, first + 2},
		{"oldest-1 resumes the whole ring", strconv.FormatUint(first-1, 10), []string{"snapshot", "upsert", "upsert"}, first},
		{"newest id gets nothing", strconv.FormatUint(first+2, 10), []string{}, 0},
		{"older than the ring gets a snapshot", "1", []string{EventSnapshot}, first + 2},
		{"newer than the hub gets a snapshot", strconv.FormatUint(first+99, 10), []string{EventSnapshot}, first + 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sub, err := h.Subscribe(tc.lastID)
			if err != nil {
				t.Fatal(err)
			}
			defer sub.Close()
			if got := types(sub.Initial); !equal(got, tc.want) {
				t.Fatalf("initial = %v, want %v", got, tc.want)
			}
			if len(sub.Initial) > 0 && sub.Initial[0].ID != tc.wantFirstID {
				t.Errorf("first id = %d, want %d", sub.Initial[0].ID, tc.wantFirstID)
			}
		})
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSnapshotFollowsDeltas: a late subscriber's snapshot is the view after every event, so it
// never needs events from before its snapshot id.
func TestSnapshotFollowsDeltas(t *testing.T) {
	h, _ := newTestHub(t, HubOptions{})
	publish(t, h, upsert("core/Pod/ns/b", "Running"),
		model.Event{Type: string(model.OpDelete), Data: model.IDData{ID: "core/Pod/ns/a"}},
		model.Event{Type: EventCapabilities, Data: CapabilitiesData{Capabilities: map[string]model.Capability{"kubelet": {State: "ok"}}}},
		model.Event{Type: EventCapabilities, Data: CapabilitiesData{Capabilities: map[string]model.Capability{"hubble-relay": {State: "ok"}}}},
		model.Event{Type: EventMetrics, Data: MetricsData{SizeBy: SizeByPods, Samples: []Sample{}}})
	sub, err := h.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	var snap model.SnapshotData
	if err := json.Unmarshal(sub.Initial[0].Data, &snap); err != nil {
		t.Fatal(err)
	}
	if len(snap.Entities) != 1 || snap.Entities[0].ID != "core/Pod/ns/b" {
		t.Errorf("snapshot entities = %+v, want only core/Pod/ns/b", snap.Entities)
	}
	if snap.Capabilities["kubelet"].State != "ok" || snap.Capabilities["hubble-relay"].State != "ok" {
		t.Errorf("snapshot capabilities = %+v, want both kubelet and hubble-relay ok", snap.Capabilities)
	}
}

func TestRingRetentionAndCap(t *testing.T) {
	h, c := newTestHub(t, HubOptions{Retention: time.Minute, MaxEvents: 3})
	start := h.last
	c.t = c.t.Add(2 * time.Minute)
	publish(t, h, upsert("core/Pod/ns/b", "x")) // the snapshot entry is now past retention
	if len(h.ring) != 1 || h.ring[0].ID != start+1 {
		t.Fatalf("after retention: ring ids %v, want [%d]", ids(h.ring), start+1)
	}
	publish(t, h, upsert("core/Pod/ns/b", "y"), upsert("core/Pod/ns/b", "z"), upsert("core/Pod/ns/b", "w"))
	if len(h.ring) != 3 || h.ring[0].ID != start+2 {
		t.Fatalf("after cap: ring ids %v, want 3 from %d", ids(h.ring), start+2)
	}
	sub, err := h.Subscribe(strconv.FormatUint(start, 10)) // trimmed away
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	if got := types(sub.Initial); !equal(got, []string{EventSnapshot}) {
		t.Errorf("resume from a trimmed id = %v, want a snapshot", got)
	}
}

func ids(es []Entry) []uint64 {
	out := make([]uint64, len(es))
	for i, e := range es {
		out[i] = e.ID
	}
	return out
}

func TestSlowSubscriberDropped(t *testing.T) {
	h, _ := newTestHub(t, HubOptions{SubBuffer: 2})
	slow, err := h.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Close()
	publish(t, h, upsert("core/Pod/ns/b", "1"), upsert("core/Pod/ns/b", "2"), upsert("core/Pod/ns/b", "3"))
	n := 0
	for range slow.C { // closed by the hub once the third event did not fit
		n++
	}
	if n != 2 {
		t.Errorf("slow subscriber got %d events before being dropped, want 2", n)
	}
	if len(h.subs) != 0 {
		t.Errorf("hub still holds %d subscribers", len(h.subs))
	}
}

// TestIDsStartAboveEarlierProcess: a browser resuming after a restart carries an id from the
// old process, which must not match the new ring.
func TestIDsStartAboveEarlierProcess(t *testing.T) {
	old, _ := newTestHub(t, HubOptions{})
	publish(t, old, upsert("core/Pod/ns/b", "x"))
	c := &clock{t: time.Date(2026, 11, 10, 9, 0, 1, 0, time.UTC)}
	h := NewHub(HubOptions{Now: c.now})
	publish(t, h, snapshotOf())
	sub, err := h.Subscribe(strconv.FormatUint(old.last, 10))
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	if got := types(sub.Initial); !equal(got, []string{EventSnapshot}) {
		t.Errorf("resume with an earlier process's id = %v, want a snapshot", got)
	}
}

func TestPublishUnencodableIsAtomic(t *testing.T) {
	h, _ := newTestHub(t, HubOptions{})
	before := h.last
	err := h.Publish(upsert("core/Pod/ns/b", "x"), model.Event{Type: "bad", Data: func() {}})
	if err == nil || h.last != before {
		t.Fatalf("Publish(unencodable) err = %v, last moved %d → %d; want an error and nothing published", err, before, h.last)
	}
}
