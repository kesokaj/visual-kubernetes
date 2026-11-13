package demo

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	"github.com/kesokaj/visual-kubernetes/internal/stream"
)

// ReplayServices implements ServiceStore in --replay --demo mode and emits SSE deltas to Hub.
type ReplayServices struct {
	Hub  *stream.Hub
	mu   sync.Mutex
	byID map[string]*replayServiceState
}

type replayServiceState struct {
	rv        int
	svcType   string
	clusterIP string
}

// GetServiceType reads a Service's exposure state from Hub.
func (r *ReplayServices) GetServiceType(_ context.Context, ref model.Ref) (ServiceTypeResponse, *StatusErr) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, _, errSt := r.loadLocked(ref)
	if errSt != nil {
		return ServiceTypeResponse{}, errSt
	}
	return ServiceTypeResponse{
		ID:              ref.ID(),
		ResourceVersion: strconv.Itoa(st.rv),
		Type:            st.svcType,
		AllowedTypes:    allowedServiceTypes(),
		ClusterIP:       st.clusterIP,
		Headless:        false,
	}, nil
}

// PatchServiceType updates the in-memory Service type and publishes SSE deltas to Hub.
func (r *ReplayServices) PatchServiceType(_ context.Context, ref model.Ref, req PatchServiceTypeRequest) (ServiceTypeResponse, *StatusErr) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ent, errSt := r.loadLocked(ref)
	if errSt != nil {
		return ServiceTypeResponse{}, errSt
	}
	if strconv.Itoa(st.rv) != req.ResourceVersion {
		return ServiceTypeResponse{}, &StatusErr{Code: http.StatusConflict, Msg: resourceConflictMessage}
	}
	st.svcType = req.Type
	st.rv++
	ent.Status = req.Type
	_ = r.Hub.Publish(model.Event{Type: string(model.OpUpsert), Data: model.EntityData{Entity: ent}})
	reconcileReplayInternet(r.Hub)
	return ServiceTypeResponse{
		ID:              ref.ID(),
		ResourceVersion: strconv.Itoa(st.rv),
		Type:            st.svcType,
		AllowedTypes:    allowedServiceTypes(),
		ClusterIP:       st.clusterIP,
		Headless:        false,
	}, nil
}

func (r *ReplayServices) loadLocked(ref model.Ref) (*replayServiceState, model.Entity, *StatusErr) {
	if r == nil || r.Hub == nil {
		return nil, model.Entity{}, &StatusErr{Code: http.StatusNotFound, Msg: "service not found"}
	}
	id := ref.ID()
	ent, ok := r.Hub.Entity(id)
	if !ok || ent.Folded || ent.Group != "core" || ent.Kind != "Service" {
		return nil, model.Entity{}, &StatusErr{Code: http.StatusNotFound, Msg: "service not found"}
	}
	if ent.Status == string(corev1.ServiceTypeExternalName) {
		return nil, model.Entity{}, &StatusErr{Code: http.StatusBadRequest, Msg: "ExternalName Service cannot switch type"}
	}
	if ent.Status == corev1.ClusterIPNone || strings.Contains(strings.ToLower(ent.Status), "headless") {
		return nil, model.Entity{}, &StatusErr{Code: http.StatusBadRequest, Msg: "headless Service (clusterIP: None) cannot switch type"}
	}
	if r.byID != nil {
		if st, ok := r.byID[id]; ok {
			return st, ent, nil
		}
	}
	if r.byID == nil {
		r.byID = map[string]*replayServiceState{}
	}
	svcType := ent.Status
	if svcType == "" {
		svcType = string(corev1.ServiceTypeClusterIP)
	}
	st := &replayServiceState{rv: 1, svcType: svcType, clusterIP: "10.96.0.10"}
	r.byID[id] = st
	return st, ent, nil
}

func reconcileReplayInternet(hub *stream.Hub) {
	ents, edges := hub.Snapshot()
	desired := map[string]bool{}
	for _, e := range ents {
		if e.Folded || !isReplayEntrypoint(e) {
			continue
		}
		target := e.ID
		if e.AppID != "" {
			target = e.AppID
		}
		desired[target] = true
	}
	for _, edge := range edges {
		if edge.Kind == model.EdgeRoutes && edge.From == model.InternetID && !desired[edge.To] {
			_ = hub.Publish(model.Event{Type: string(model.OpEdgeDelete), Data: model.IDData{ID: edge.ID}})
		}
	}
	if len(desired) == 0 {
		if _, ok := hub.Entity(model.InternetID); ok {
			_ = hub.Publish(model.Event{Type: string(model.OpDelete), Data: model.IDData{ID: model.InternetID}})
		}
		return
	}
	status := fmt.Sprintf("%d entrypoints", len(desired))
	if len(desired) == 1 {
		status = "1 entrypoint"
	}
	if cur, ok := hub.Entity(model.InternetID); !ok || cur.Status != status {
		_ = hub.Publish(model.Event{
			Type: string(model.OpUpsert),
			Data: model.EntityData{Entity: model.Entity{
				ID: model.InternetID, Kind: "Internet", Group: "ext", Name: "internet",
				Status: status, FilterGroup: model.Ingress,
			}},
		})
	}
	for _, target := range slices.Sorted(maps.Keys(desired)) {
		edgeID := fmt.Sprintf("%s:%s->%s", model.EdgeRoutes, model.InternetID, target)
		if !slices.ContainsFunc(edges, func(ed model.Edge) bool { return ed.ID == edgeID }) {
			_ = hub.Publish(model.Event{
				Type: string(model.OpEdgeUpsert),
				Data: model.EdgeData{Edge: model.Edge{ID: edgeID, Kind: model.EdgeRoutes, From: model.InternetID, To: target}},
			})
		}
	}
}

func isReplayEntrypoint(e model.Entity) bool {
	switch {
	case e.Group == "core" && e.Kind == "Service":
		return e.Status == string(corev1.ServiceTypeLoadBalancer) || e.Status == string(corev1.ServiceTypeNodePort)
	case e.Group == "networking.k8s.io" && e.Kind == "Ingress":
		return true
	case e.Group == "gateway.networking.k8s.io" && e.Kind == "Gateway":
		return true
	default:
		return false
	}
}
