package model

import (
	"maps"
	"slices"
)

// diff returns the deltas that turn prev into next, in Apply's documented order.
func diff(prev, next view) []Delta {
	var out []Delta
	for _, id := range slices.Sorted(maps.Keys(next.entities)) {
		e := next.entities[id]
		if old, ok := prev.entities[id]; !ok || !entityEqual(old, e) {
			out = append(out, Delta{Op: OpUpsert, Entity: &e, ID: id})
		}
	}
	for _, id := range slices.Sorted(maps.Keys(next.edges)) {
		if _, ok := prev.edges[id]; !ok {
			e := next.edges[id]
			out = append(out, Delta{Op: OpEdgeUpsert, Edge: &e, ID: id})
		}
	}
	for _, id := range slices.Sorted(maps.Keys(prev.edges)) {
		if _, ok := next.edges[id]; !ok {
			out = append(out, Delta{Op: OpEdgeDelete, ID: id})
		}
	}
	for _, id := range slices.Sorted(maps.Keys(prev.entities)) {
		if _, ok := next.entities[id]; !ok {
			out = append(out, Delta{Op: OpDelete, ID: id})
		}
	}
	return out
}

// entityEqual compares every field; a field added to Entity must be added here too
// (TestEntityEqualCoversEveryField fails otherwise).
func entityEqual(a, b Entity) bool {
	return a.ID == b.ID && a.Kind == b.Kind && a.Group == b.Group && a.Name == b.Name &&
		a.Namespace == b.Namespace && a.Status == b.Status && a.NodeName == b.NodeName &&
		a.OwnerID == b.OwnerID && a.AppID == b.AppID && a.FilterGroup == b.FilterGroup &&
		a.Folded == b.Folded && a.Count == b.Count && maps.Equal(a.Labels, b.Labels)
}
