package stream

import (
	"maps"
	"slices"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

// view is the state a client holds after applying every event published so far. The hub keeps
// it under its own lock, in step with the event ids, so a subscriber's snapshot is exactly the
// state after the last id it is told about.
type view struct {
	entities map[string]model.Entity
	edges    map[string]model.Edge
	caps     map[string]model.Capability
}

func newView() view {
	return view{entities: map[string]model.Entity{}, edges: map[string]model.Edge{}, caps: map[string]model.Capability{}}
}

// apply updates the view for the event types that change state; any other type (metrics,
// event, chain, flow) passes through the stream without changing it.
func (v *view) apply(ev model.Event) {
	switch d := ev.Data.(type) {
	case model.SnapshotData:
		*v = newView()
		for _, e := range d.Entities {
			v.entities[e.ID] = e
		}
		for _, e := range d.Edges {
			v.edges[e.ID] = e
		}
		maps.Copy(v.caps, d.Capabilities)
	case model.EntityData:
		v.entities[d.Entity.ID] = d.Entity
	case model.EdgeData:
		v.edges[d.Edge.ID] = d.Edge
	case model.IDData:
		switch ev.Type {
		case string(model.OpDelete):
			delete(v.entities, d.ID)
		case string(model.OpEdgeDelete):
			delete(v.edges, d.ID)
		}
	case CapabilitiesData:
		if v.caps == nil {
			v.caps = map[string]model.Capability{}
		}
		maps.Copy(v.caps, d.Capabilities)
	}
}

// snapshot returns the view as `snapshot` data, entities and edges sorted by id.
func (v *view) snapshot() model.SnapshotData {
	ents := make([]model.Entity, 0, len(v.entities))
	for _, id := range slices.Sorted(maps.Keys(v.entities)) {
		ents = append(ents, v.entities[id])
	}
	edges := make([]model.Edge, 0, len(v.edges))
	for _, id := range slices.Sorted(maps.Keys(v.edges)) {
		edges = append(edges, v.edges[id])
	}
	return model.SnapshotData{Entities: ents, Edges: edges, Capabilities: maps.Clone(v.caps)}
}
