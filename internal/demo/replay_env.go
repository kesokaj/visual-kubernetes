package demo

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"

	corev1 "k8s.io/api/core/v1"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	"github.com/kesokaj/visual-kubernetes/internal/stream"
)

type replayWorkloadState struct {
	rv         int
	replicas   int32
	ready      int32
	containers []corev1.Container
}

// ReplayWorkloads is an in-memory WorkloadStore backed by a replay Hub's entities so --replay
// --demo exercises the full GET/PATCH/409/readOnlySource and replica scale contracts without a cluster.
type ReplayWorkloads struct {
	Hub  *stream.Hub
	mu   sync.Mutex
	byID map[string]*replayWorkloadState
}

// GetWorkloadEnv returns the replay workload's current container env state, seeding it on first
// read when the workload exists in Hub.
func (r *ReplayWorkloads) GetWorkloadEnv(_ context.Context, ref model.Ref) (WorkloadEnvResponse, *StatusErr) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.loadLocked(ref)
	if !ok {
		return WorkloadEnvResponse{}, &StatusErr{Code: http.StatusNotFound, Msg: "workload not found"}
	}
	return WorkloadEnvResponse{
		ID:              ref.ID(),
		ResourceVersion: strconv.Itoa(st.rv),
		Containers:      extractContainers(st.containers),
	}, nil
}

// PatchWorkloadEnv applies the same optimistic resourceVersion check and valueFrom preservation
// rules as KubeWorkloads against the in-memory replay state.
func (r *ReplayWorkloads) PatchWorkloadEnv(_ context.Context, ref model.Ref, req PatchWorkloadEnvRequest) (WorkloadEnvResponse, *StatusErr) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.loadLocked(ref)
	if !ok {
		return WorkloadEnvResponse{}, &StatusErr{Code: http.StatusNotFound, Msg: "workload not found"}
	}
	if strconv.Itoa(st.rv) != req.ResourceVersion {
		return WorkloadEnvResponse{}, &StatusErr{Code: http.StatusConflict, Msg: conflictMessage}
	}
	next := make([]corev1.Container, len(st.containers))
	for i := range st.containers {
		next[i] = *st.containers[i].DeepCopy()
	}
	if errSt := applyContainerPatch(next, req); errSt != nil {
		return WorkloadEnvResponse{}, errSt
	}
	st.containers = next
	st.rv++
	return WorkloadEnvResponse{
		ID:              ref.ID(),
		ResourceVersion: strconv.Itoa(st.rv),
		Containers:      extractContainers(st.containers),
	}, nil
}

// GetWorkloadScale returns the replay workload's replica count and HPA warning state.
func (r *ReplayWorkloads) GetWorkloadScale(_ context.Context, ref model.Ref) (WorkloadScaleResponse, *StatusErr) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.loadLocked(ref)
	if !ok {
		return WorkloadScaleResponse{}, &StatusErr{Code: http.StatusNotFound, Msg: "workload not found"}
	}
	return buildWorkloadScaleResponse(ref, strconv.Itoa(st.rv), st.replicas, r.findManagingHPA(ref)), nil
}

// PatchWorkloadScale updates the replay workload's replica count and publishes an upsert delta.
func (r *ReplayWorkloads) PatchWorkloadScale(_ context.Context, ref model.Ref, req PatchWorkloadScaleRequest) (WorkloadScaleResponse, *StatusErr) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.loadLocked(ref)
	if !ok {
		return WorkloadScaleResponse{}, &StatusErr{Code: http.StatusNotFound, Msg: "workload not found"}
	}
	if strconv.Itoa(st.rv) != req.ResourceVersion {
		return WorkloadScaleResponse{}, &StatusErr{Code: http.StatusConflict, Msg: resourceConflictMessage}
	}
	st.replicas = *req.Replicas
	st.rv++
	appID := model.AppID(ref.Namespace, ref.Kind, ref.Name)
	if r.Hub != nil {
		if ent, ok := r.Hub.Entity(appID); ok {
			ent.Status = fmt.Sprintf("%d/%d ready", min(st.ready, st.replicas), st.replicas)
			_ = r.Hub.Publish(model.Event{Type: string(model.OpUpsert), Data: model.EntityData{Entity: ent}})
		}
	}
	return buildWorkloadScaleResponse(ref, strconv.Itoa(st.rv), st.replicas, r.findManagingHPA(ref)), nil
}

func (r *ReplayWorkloads) loadLocked(ref model.Ref) (*replayWorkloadState, bool) {
	id := ref.ID()
	if r.byID != nil {
		if st, ok := r.byID[id]; ok {
			return st, true
		}
	}
	if !r.existsInHub(id) {
		return nil, false
	}
	if r.byID == nil {
		r.byID = make(map[string]*replayWorkloadState)
	}
	containers := []corev1.Container{
		{
			Name: ref.Name,
			Env: []corev1.EnvVar{
				{Name: "LOG_LEVEL", Value: "info"},
				{
					Name: "API_SECRET",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{Name: ref.Name + "-secrets"},
							Key:                  "api-key",
						},
					},
				},
			},
		},
	}
	if ref.Kind == "DaemonSet" {
		containers = append(containers, corev1.Container{
			Name: "metrics-exporter",
			Env:  []corev1.EnvVar{{Name: "METRICS_PORT", Value: "9100"}},
		})
	}
	ready, desired := r.seedScaleFromHub(ref)
	st := &replayWorkloadState{rv: 1, replicas: desired, ready: ready, containers: containers}
	r.byID[id] = st
	return st, true
}

func (r *ReplayWorkloads) seedScaleFromHub(ref model.Ref) (ready, desired int32) {
	if r == nil || r.Hub == nil {
		return 1, 1
	}
	ent, ok := r.Hub.Entity(model.AppID(ref.Namespace, ref.Kind, ref.Name))
	if !ok {
		return 1, 1
	}
	var gotReady, gotDesired int32
	if n, err := fmt.Sscanf(ent.Status, "%d/%d ready", &gotReady, &gotDesired); err == nil && n == 2 && gotReady >= 0 && gotDesired >= 0 {
		return gotReady, gotDesired
	}
	return 1, 1
}

func (r *ReplayWorkloads) findManagingHPA(ref model.Ref) string {
	if r == nil || r.Hub == nil {
		return ""
	}
	appID := model.AppID(ref.Namespace, ref.Kind, ref.Name)
	ents, _ := r.Hub.Snapshot()
	for _, e := range ents {
		if !e.Folded && e.Group == "autoscaling" && e.Kind == "HorizontalPodAutoscaler" && e.Namespace == ref.Namespace && e.AppID == appID {
			return e.Name
		}
	}
	return ""
}

func (r *ReplayWorkloads) existsInHub(id string) bool {
	if r == nil || r.Hub == nil {
		return false
	}
	ents, _ := r.Hub.Snapshot()
	for _, e := range ents {
		if e.Folded {
			continue
		}
		if e.ID == id || e.OwnerID == id {
			return true
		}
	}
	return false
}
