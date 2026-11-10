// Package model turns the cluster objects the watches deliver into the live view the page
// draws: entities with D13 ids, one app bubble per top owner with its pods and satellites (D15),
// the edges between them, and `counter/*` entities for what S5 folds or the page does not draw.
// It holds no client and starts no goroutine; one mutex makes Apply, Snapshot and Object safe
// to call from the watch pump and the stream readers at once.
package model

import (
	"maps"
	"slices"
	"sync"

	"k8s.io/apimachinery/pkg/runtime"
)

// FilterGroup is one of the eight filter-pill groups (design/mockup-v3.html:1616-1623).
type FilterGroup string

// The eight filter groups, all on by default in the page (plan D3).
const (
	ControlPlane   FilterGroup = "controlPlane"
	Nodes          FilterGroup = "nodes"
	Workloads      FilterGroup = "workloads"
	Pods           FilterGroup = "pods"
	ServicesConfig FilterGroup = "servicesConfig"
	Traffic        FilterGroup = "traffic"
	Ingress        FilterGroup = "ingress"
	System         FilterGroup = "system"
)

// Entity is one thing the page can draw, in the SSE wire shape (plan Interfaces § SSE events).
// For an app bubble, OwnerID is the workload object the app stands for, so the panel can fetch
// its config; for a pod it is the pod's controller. Count is set only on `counter/*` entities.
type Entity struct {
	ID          string            `json:"id"`
	Kind        string            `json:"kind"`
	Group       string            `json:"group"`
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	Labels      map[string]string `json:"labels"`
	Status      string            `json:"status"`
	NodeName    string            `json:"nodeName,omitempty"`
	OwnerID     string            `json:"ownerId,omitempty"`
	AppID       string            `json:"appId,omitempty"`
	FilterGroup FilterGroup       `json:"filterGroup"`
	Folded      bool              `json:"folded,omitempty"`
	Count       int               `json:"count,omitempty"`
}

// EdgeKind says why two entities are linked.
type EdgeKind string

// Edge kinds. A satellite that serves exactly one app carries that app's id in AppID instead
// of an edge; edges are for links that bubbles alone cannot show (D15).
const (
	EdgeMember     EdgeKind = "member"     // cp/apiserver → node
	EdgeHosts      EdgeKind = "hosts"      // node → app with at least one pod on it
	EdgeSelects    EdgeKind = "selects"    // Service or NetworkPolicy → each of several apps
	EdgeReferences EdgeKind = "references" // ConfigMap or PVC → each of several apps
	EdgeRoutes     EdgeKind = "routes"     // Ingress or HTTPRoute → app or Service
	EdgeParent     EdgeKind = "parent"     // Gateway → HTTPRoute
)

// Edge links two entity ids. ID is derived from the other three fields, so two edges with the
// same ID are equal.
type Edge struct {
	ID   string   `json:"id"`
	From string   `json:"from"`
	To   string   `json:"to"`
	Kind EdgeKind `json:"kind"`
}

func newEdge(kind EdgeKind, from, to string) Edge {
	return Edge{ID: string(kind) + ":" + from + "->" + to, From: from, To: to, Kind: kind}
}

// Change is one object added, updated or deleted, as a watch delivers it. Obj is typed for the
// kinds client-go knows and *unstructured.Unstructured otherwise; the model never mutates it.
type Change struct {
	Obj     runtime.Object
	Deleted bool
}

// DeltaOp is what a Delta does to the view.
type DeltaOp string

// Delta operations; each maps to one SSE event type of the same name (events.go).
const (
	OpUpsert     DeltaOp = "upsert"
	OpDelete     DeltaOp = "delete"
	OpEdgeUpsert DeltaOp = "edgeUpsert"
	OpEdgeDelete DeltaOp = "edgeDelete"
)

// Delta is one change to the view: Entity for OpUpsert, Edge for OpEdgeUpsert, ID otherwise.
type Delta struct {
	Op     DeltaOp
	Entity *Entity
	Edge   *Edge
	ID     string
}

// ignoredKinds are never stored. Events are streamed as `event` messages by ApplyEvents
// (k8sevent.go), never kept; Secrets are never watched (R2) and are dropped here too should
// one ever arrive.
var ignoredKinds = map[string]bool{"core/Event": true, "events.k8s.io/Event": true, "core/Secret": true}

type view struct {
	entities map[string]Entity
	edges    map[string]Edge
}

// Model is the live view. The zero value is not usable; call New.
type Model struct {
	mu   sync.Mutex
	objs map[string]runtime.Object
	view view
}

// New returns an empty model; its view already holds the cp/apiserver hub.
func New() *Model {
	m := &Model{objs: map[string]runtime.Object{}}
	m.view = derive(m.objs)
	return m
}

// Apply stores the changes, recomputes the view and returns what changed, in the order a
// client should apply it: entity upserts, edge upserts, edge deletes, entity deletes, each
// sorted by id. Objects the model cannot identify, and Events, are dropped.
func (m *Model) Apply(changes ...Change) []Delta {
	m.mu.Lock()
	defer m.mu.Unlock()
	stored := false
	for _, c := range changes {
		ref, ok := identify(c.Obj)
		if !ok || ignoredKinds[ref.Group+"/"+ref.Kind] {
			continue
		}
		if c.Deleted {
			delete(m.objs, ref.ID())
		} else {
			m.objs[ref.ID()] = c.Obj
		}
		stored = true
	}
	if !stored {
		return nil
	}
	next := derive(m.objs)
	deltas := diff(m.view, next)
	m.view = next
	return deltas
}

// Snapshot returns every entity and edge, each sorted by id.
func (m *Model) Snapshot() ([]Entity, []Edge) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ents := make([]Entity, 0, len(m.view.entities))
	for _, id := range slices.Sorted(maps.Keys(m.view.entities)) {
		ents = append(ents, m.view.entities[id])
	}
	edges := make([]Edge, 0, len(m.view.edges))
	for _, id := range slices.Sorted(maps.Keys(m.view.edges)) {
		edges = append(edges, m.view.edges[id])
	}
	return ents, edges
}

// Object returns the stored object for a D13 object id (any stored kind, drawn or not). The
// object is shared with the watch cache: callers must not mutate it.
func (m *Model) Object(id string) (runtime.Object, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	obj, ok := m.objs[id]
	return obj, ok
}
