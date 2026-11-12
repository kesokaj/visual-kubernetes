package demo

import (
	"context"
	"net/http"
	"strconv"
	"sync"

	corev1 "k8s.io/api/core/v1"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	"github.com/kesokaj/visual-kubernetes/internal/stream"
)

type replayWorkloadState struct {
	rv         int
	containers []corev1.Container
}

// ReplayWorkloads is an in-memory WorkloadStore backed by a replay Hub's entities so --replay
// --demo exercises the full GET/PATCH/409/readOnlySource contract without a cluster.
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
	st := &replayWorkloadState{rv: 1, containers: containers}
	r.byID[id] = st
	return st, true
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
