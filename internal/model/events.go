package model

// Event is one SSE event (plan Interfaces § SSE events); Type is the SSE `event:` name and
// Data marshals to its `data:` JSON.
type Event struct {
	Type string
	Data any
}

// Capability is one source's state in the snapshot, using the probe's five states.
type Capability struct {
	State  string `json:"state"`
	Detail string `json:"detail"`
}

// SnapshotData is the `snapshot` event's data.
type SnapshotData struct {
	Entities     []Entity              `json:"entities"`
	Edges        []Edge                `json:"edges"`
	Capabilities map[string]Capability `json:"capabilities"`
}

// EntityData is the `upsert` event's data.
type EntityData struct {
	Entity Entity `json:"entity"`
}

// EdgeData is the `edgeUpsert` event's data.
type EdgeData struct {
	Edge Edge `json:"edge"`
}

// IDData is the `delete` and `edgeDelete` events' data.
type IDData struct {
	ID string `json:"id"`
}

// Record is one line of a recorded stream file (plan D14): an SSE event and its offset in
// milliseconds from the start of the recording. The first record is always a snapshot.
type Record struct {
	AtMs  int64  `json:"atMs"`
	Event string `json:"event"`
	Data  any    `json:"data"`
}

// SnapshotEvent builds the `snapshot` event from the model's current view.
func (m *Model) SnapshotEvent(caps map[string]Capability) Event {
	ents, edges := m.Snapshot()
	if caps == nil {
		caps = map[string]Capability{}
	}
	return Event{Type: "snapshot", Data: SnapshotData{Entities: ents, Edges: edges, Capabilities: caps}}
}

// DeltaEvents maps deltas to SSE events one for one, keeping their order.
func DeltaEvents(ds []Delta) []Event {
	out := make([]Event, 0, len(ds))
	for _, d := range ds {
		switch d.Op {
		case OpUpsert:
			out = append(out, Event{Type: string(d.Op), Data: EntityData{Entity: *d.Entity}})
		case OpEdgeUpsert:
			out = append(out, Event{Type: string(d.Op), Data: EdgeData{Edge: *d.Edge}})
		default:
			out = append(out, Event{Type: string(d.Op), Data: IDData{ID: d.ID}})
		}
	}
	return out
}
