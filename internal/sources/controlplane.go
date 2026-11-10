package sources

import (
	"fmt"
	"maps"
	"strings"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

// Control-plane entity ids. The model owns cp/apiserver as the hub of the web; the collector
// re-publishes it with a status, and adds the parts the API server does not expose as objects
// (PRD S4: shown as inferred, with the evidence that inferred them).
const (
	etcdID              = "cp/etcd"
	schedulerID         = "cp/scheduler"
	controllerManagerID = "cp/controller-manager"
)

// EvidenceLabel says what a control-plane entity's status rests on: lease, readyz or none. The
// page shows an "inferred" badge on every cp/* entity and this label in its panel.
const EvidenceLabel = "visual-kubernetes/evidence"

// Evidence values.
const (
	evidenceLease  = "lease"
	evidenceReadyz = "readyz"
	evidenceNone   = "none"
)

// leaderLeases are the leader-election Leases of the two controllers, by entity id.
var leaderLeases = map[string]string{
	schedulerID:         model.ID("coordination.k8s.io", "Lease", "kube-system", "kube-scheduler"),
	controllerManagerID: model.ID("coordination.k8s.io", "Lease", "kube-system", "kube-controller-manager"),
}

func cpEntity(id, kind, name, status, evidence string) model.Entity {
	return model.Entity{ID: id, Kind: kind, Group: "cp", Name: name, Status: status,
		Labels: map[string]string{EvidenceLabel: evidence}, FilterGroup: model.ControlPlane}
}

// controlPlane builds the four control-plane entities and the edges from the API server to the
// other three.
func (c *Collector) controlPlane(now time.Time, rz readyzResult) ([]model.Entity, []model.Edge) {
	ents := []model.Entity{
		c.apiserverEntity(rz),
		etcdEntity(rz),
		c.leaseEntity(schedulerID, "Scheduler", "scheduler", now),
		c.leaseEntity(controllerManagerID, "ControllerManager", "controller-manager", now),
	}
	edges := make([]model.Edge, 0, len(ents)-1)
	for _, e := range ents[1:] {
		edges = append(edges, model.Edge{ID: string(model.EdgeMember) + ":" + model.APIServerID + "->" + e.ID,
			From: model.APIServerID, To: e.ID, Kind: model.EdgeMember})
	}
	return ents, edges
}

func (c *Collector) apiserverEntity(rz readyzResult) model.Entity {
	status, evidence := "unknown: /readyz not readable", evidenceNone
	if rz.reached {
		evidence = evidenceReadyz
		status = "ready"
		if failed := rz.failed(); len(failed) > 0 {
			status = "not ready: " + strings.Join(failed, ", ")
		}
	}
	if c.objects != nil {
		status += fmt.Sprintf(" · %d objects", *c.objects)
	}
	return cpEntity(model.APIServerID, "APIServer", "apiserver", status, evidence)
}

// etcdEntity reads etcd's health from the API server's own readiness checks; a managed control
// plane may not list them.
func etcdEntity(rz readyzResult) model.Entity {
	for _, check := range []string{"etcd", "etcd-readiness"} {
		ok, present := rz.checks[check]
		if !present {
			continue
		}
		if !ok {
			return cpEntity(etcdID, "Etcd", "etcd", "not ready (readyz "+check+" failing)", evidenceReadyz)
		}
		return cpEntity(etcdID, "Etcd", "etcd", "ok (readyz "+check+")", evidenceReadyz)
	}
	return cpEntity(etcdID, "Etcd", "etcd", "inferred: not reported by /readyz", evidenceNone)
}

// leaseEntity reads a controller's leader-election Lease: a lease renewed within its duration
// names the current leader. Managed control planes often hide these Leases.
func (c *Collector) leaseEntity(id, kind, name string, now time.Time) model.Entity {
	obj, ok := c.d.Model.Object(leaderLeases[id])
	lease, isLease := obj.(*coordinationv1.Lease)
	if !ok || !isLease {
		return cpEntity(id, kind, name, "inferred: no leader lease visible (managed control plane?)", evidenceNone)
	}
	holder := "unknown"
	if h := lease.Spec.HolderIdentity; h != nil && *h != "" {
		// holderIdentity is "<node>_<uuid>"; the node is what a presenter recognises.
		holder = *h
		if i := strings.LastIndexByte(holder, '_'); i > 0 {
			holder = holder[:i]
		}
	}
	if fresh(lease, now) {
		return cpEntity(id, kind, name, "leader "+holder, evidenceLease)
	}
	return cpEntity(id, kind, name, "lease expired (last leader "+holder+")", evidenceLease)
}

func fresh(l *coordinationv1.Lease, now time.Time) bool {
	if l.Spec.RenewTime == nil || l.Spec.LeaseDurationSeconds == nil {
		return false
	}
	return now.Sub(l.Spec.RenewTime.Time) <= time.Duration(*l.Spec.LeaseDurationSeconds)*time.Second
}

// controlPlaneEvents returns the edges once, then an upsert for each entity whose status or
// labels changed since it was last published.
func (c *Collector) controlPlaneEvents(ents []model.Entity, edges []model.Edge) []model.Event {
	var out []model.Event
	if !c.cpEdges {
		for _, e := range edges {
			out = append(out, model.Event{Type: string(model.OpEdgeUpsert), Data: model.EdgeData{Edge: e}})
		}
		c.cpEdges = true
	}
	for _, e := range ents {
		prev, seen := c.cp[e.ID]
		if seen && prev.Status == e.Status && maps.Equal(prev.Labels, e.Labels) {
			continue
		}
		c.cp[e.ID] = e
		out = append(out, model.Event{Type: string(model.OpUpsert), Data: model.EntityData{Entity: e}})
	}
	return out
}
