package demo

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	"github.com/kesokaj/visual-kubernetes/internal/stream"
)

// ReplayAutoscalers implements AutoscalerStore in --replay --demo mode and emits SSE deltas to Hub.
type ReplayAutoscalers struct {
	Hub  *stream.Hub
	mu   sync.Mutex
	byID map[string]*replayAutoscalerState
}

type replayAutoscalerState struct {
	rv              int
	minReplicas     int32
	maxReplicas     int32
	currentReplicas int32
	updateMode      string
	containers      []VPAContainerPolicy
}

// GetAutoscaler reads an HPA or VPA from Hub.
func (r *ReplayAutoscalers) GetAutoscaler(_ context.Context, ref model.Ref) (AutoscalerResponse, *StatusErr) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, _, ok := r.loadLocked(ref)
	if !ok {
		return AutoscalerResponse{}, &StatusErr{Code: http.StatusNotFound, Msg: "autoscaler not found"}
	}
	return buildReplayAutoscalerResponse(ref, st), nil
}

// PatchAutoscaler updates in-memory HPA/VPA state and publishes an upsert delta to Hub.
func (r *ReplayAutoscalers) PatchAutoscaler(_ context.Context, ref model.Ref, req PatchAutoscalerRequest) (AutoscalerResponse, *StatusErr) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ent, ok := r.loadLocked(ref)
	if !ok {
		return AutoscalerResponse{}, &StatusErr{Code: http.StatusNotFound, Msg: "autoscaler not found"}
	}
	if strconv.Itoa(st.rv) != req.ResourceVersion {
		return AutoscalerResponse{}, &StatusErr{Code: http.StatusConflict, Msg: resourceConflictMessage}
	}
	st.rv++
	if ref.Kind == "HorizontalPodAutoscaler" {
		st.minReplicas = *req.HPA.MinReplicas
		st.maxReplicas = *req.HPA.MaxReplicas
		ent.Status = fmt.Sprintf("%d/%d–%d", st.currentReplicas, st.minReplicas, st.maxReplicas)
	} else {
		st.updateMode = req.VPA.UpdateMode
		st.containers = slices.Clone(req.VPA.Containers)
		ent.Status = st.updateMode
	}
	_ = r.Hub.Publish(model.Event{Type: string(model.OpUpsert), Data: model.EntityData{Entity: ent}})
	return buildReplayAutoscalerResponse(ref, st), nil
}

func buildReplayAutoscalerResponse(ref model.Ref, st *replayAutoscalerState) AutoscalerResponse {
	if ref.Kind == "HorizontalPodAutoscaler" {
		return AutoscalerResponse{
			ID:              ref.ID(),
			Kind:            ref.Kind,
			ResourceVersion: strconv.Itoa(st.rv),
			HPA: &HPAResponse{
				MinReplicas:     st.minReplicas,
				MaxReplicas:     st.maxReplicas,
				CurrentReplicas: st.currentReplicas,
			},
		}
	}
	return AutoscalerResponse{
		ID:              ref.ID(),
		Kind:            ref.Kind,
		ResourceVersion: strconv.Itoa(st.rv),
		VPA: &VPAResponse{
			UpdateMode:   st.updateMode,
			AllowedModes: allowedVPAModeCopy(),
			Containers:   slices.Clone(st.containers),
		},
	}
}

func (r *ReplayAutoscalers) loadLocked(ref model.Ref) (*replayAutoscalerState, model.Entity, bool) {
	if r == nil || r.Hub == nil {
		return nil, model.Entity{}, false
	}
	id := ref.ID()
	ent, ok := r.Hub.Entity(id)
	if !ok || ent.Folded || ent.Group != ref.Group || ent.Kind != ref.Kind {
		return nil, model.Entity{}, false
	}
	if r.byID != nil {
		if st, ok := r.byID[id]; ok {
			return st, ent, true
		}
	}
	if r.byID == nil {
		r.byID = map[string]*replayAutoscalerState{}
	}
	st := seedReplayAutoscaler(ref, ent)
	r.byID[id] = st
	return st, ent, true
}

func seedReplayAutoscaler(ref model.Ref, ent model.Entity) *replayAutoscalerState {
	if ref.Kind == "HorizontalPodAutoscaler" {
		cur, lo, hi := parseReplayHPAStatus(ent.Status)
		return &replayAutoscalerState{rv: 1, currentReplicas: cur, minReplicas: lo, maxReplicas: hi}
	}
	mode := ent.Status
	if !slices.Contains(vpaAllowedModes, mode) {
		mode = "Auto"
	}
	cname := ref.Name
	if parts := strings.Split(ent.AppID, "/"); len(parts) == 4 && parts[3] != "" {
		cname = parts[3]
	}
	return &replayAutoscalerState{
		rv:         1,
		updateMode: mode,
		containers: []VPAContainerPolicy{{ContainerName: cname, MaxCPU: "500m", MaxMemory: "256Mi"}},
	}
}

func parseReplayHPAStatus(status string) (cur, lo, hi int32) {
	if n, err := fmt.Sscanf(status, "%d replicas (min %d, max %d)", &cur, &lo, &hi); err == nil && n == 3 && lo >= 1 && hi >= lo {
		return cur, lo, hi
	}
	norm := strings.ReplaceAll(status, "–", "-")
	if n, err := fmt.Sscanf(norm, "%d/%d-%d", &cur, &lo, &hi); err == nil && n == 3 && lo >= 1 && hi >= lo {
		return cur, lo, hi
	}
	return 2, 1, 10
}
