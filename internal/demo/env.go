package demo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

const (
	// FieldManagerName identifies updates made by the opt-in --demo workload env editor.
	FieldManagerName = "visual-kubernetes-demo"

	maxEnvPatchBytes = 64 << 10
	maxPlainEnvVars  = 128
	maxEnvNameLen    = 253
)

// WorkloadEnvResponse is the JSON payload returned by GET and PATCH /api/demo/workloads/env.
type WorkloadEnvResponse struct {
	ID              string         `json:"id"`
	ResourceVersion string         `json:"resourceVersion"`
	Containers      []ContainerEnv `json:"containers"`
}

// ContainerEnv lists one container's plain and read-only environment variables.
type ContainerEnv struct {
	Name string       `json:"name"`
	Env  []EnvVarItem `json:"env"`
}

// EnvVarItem represents either an editable plain value variable or a locked valueFrom reference.
type EnvVarItem struct {
	Name           string  `json:"name"`
	Value          *string `json:"value,omitempty"`
	ReadOnlySource string  `json:"readOnlySource,omitempty"`
}

// PlainEnvVar is a single plain name/value pair submitted in PATCH /api/demo/workloads/env.
type PlainEnvVar struct {
	Name  string  `json:"name"`
	Value *string `json:"value"`
}

// PatchWorkloadEnvRequest is the JSON request body for PATCH /api/demo/workloads/env.
type PatchWorkloadEnvRequest struct {
	ID              string        `json:"id"`
	ResourceVersion string        `json:"resourceVersion"`
	Container       string        `json:"container"`
	Env             []PlainEnvVar `json:"env"`
}

// WorkloadStore reads and updates plain container env vars and replica counts on apps/v1 workloads.
type WorkloadStore interface {
	GetWorkloadEnv(ctx context.Context, ref model.Ref) (WorkloadEnvResponse, *StatusErr)
	PatchWorkloadEnv(ctx context.Context, ref model.Ref, req PatchWorkloadEnvRequest) (WorkloadEnvResponse, *StatusErr)
	GetWorkloadScale(ctx context.Context, ref model.Ref) (WorkloadScaleResponse, *StatusErr)
	PatchWorkloadScale(ctx context.Context, ref model.Ref, req PatchWorkloadScaleRequest) (WorkloadScaleResponse, *StatusErr)
}

func (s *Server) handleGetWorkloadEnv(w http.ResponseWriter, r *http.Request) {
	if s.workloads == nil {
		writePlain(w, http.StatusServiceUnavailable, "workload env editor unavailable")
		return
	}
	ref, st := parseWorkloadRef(r.URL.Query().Get("id"))
	if st != nil {
		writePlain(w, st.Code, st.Msg)
		return
	}
	resp, st := s.workloads.GetWorkloadEnv(r.Context(), ref)
	if st != nil {
		writePlain(w, st.Code, st.Msg)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePatchWorkloadEnv(w http.ResponseWriter, r *http.Request) {
	if s.workloads == nil {
		writePlain(w, http.StatusServiceUnavailable, "workload env editor unavailable")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxEnvPatchBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var req PatchWorkloadEnvRequest
	if err := dec.Decode(&req); err != nil {
		writePlain(w, http.StatusBadRequest, "invalid workload env payload")
		return
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writePlain(w, http.StatusBadRequest, "invalid workload env payload")
		return
	}
	ref, st := validatePatchRequest(req)
	if st != nil {
		writePlain(w, st.Code, st.Msg)
		return
	}
	resp, st := s.workloads.PatchWorkloadEnv(r.Context(), ref, req)
	if st != nil {
		writePlain(w, st.Code, st.Msg)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func parseWorkloadRef(rawID string) (model.Ref, *StatusErr) {
	ref, err := model.ParseID(strings.TrimSpace(rawID))
	if err != nil {
		return model.Ref{}, &StatusErr{Code: http.StatusBadRequest, Msg: "malformed workload id"}
	}
	if ref.Group != "apps" || (ref.Kind != "Deployment" && ref.Kind != "StatefulSet" && ref.Kind != "DaemonSet") {
		return model.Ref{}, &StatusErr{Code: http.StatusBadRequest, Msg: "unsupported workload kind"}
	}
	if IsProtectedNamespace(ref.Namespace) {
		return model.Ref{}, &StatusErr{Code: http.StatusForbidden, Msg: "protected namespace is refused in demo mode"}
	}
	if st := validateKubeObjectRef(ref); st != nil {
		return model.Ref{}, st
	}
	return ref, nil
}

func validatePatchRequest(req PatchWorkloadEnvRequest) (model.Ref, *StatusErr) {
	ref, st := parseWorkloadRef(req.ID)
	if st != nil {
		return model.Ref{}, st
	}
	if strings.TrimSpace(req.ResourceVersion) == "" {
		return model.Ref{}, &StatusErr{Code: http.StatusBadRequest, Msg: "resourceVersion is required"}
	}
	if strings.TrimSpace(req.Container) == "" {
		return model.Ref{}, &StatusErr{Code: http.StatusBadRequest, Msg: "container is required"}
	}
	if req.Env == nil {
		return model.Ref{}, &StatusErr{Code: http.StatusBadRequest, Msg: "env array is required"}
	}
	if len(req.Env) > maxPlainEnvVars {
		return model.Ref{}, &StatusErr{Code: http.StatusBadRequest, Msg: "at most 128 plain env vars allowed"}
	}
	seen := make(map[string]bool, len(req.Env))
	for _, item := range req.Env {
		if !isValidEnvName(item.Name) {
			return model.Ref{}, &StatusErr{Code: http.StatusBadRequest, Msg: fmt.Sprintf("invalid env var name %q", item.Name)}
		}
		if item.Value == nil {
			return model.Ref{}, &StatusErr{Code: http.StatusBadRequest, Msg: fmt.Sprintf("env var %q requires a string value", item.Name)}
		}
		if seen[item.Name] {
			return model.Ref{}, &StatusErr{Code: http.StatusBadRequest, Msg: fmt.Sprintf("duplicate env var %q", item.Name)}
		}
		seen[item.Name] = true
	}
	return ref, nil
}

func isValidEnvName(s string) bool {
	if len(s) == 0 || len(s) > maxEnvNameLen {
		return false
	}
	c0 := s[0]
	if (c0 < 'A' || c0 > 'Z') && (c0 < 'a' || c0 > 'z') && c0 != '_' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}
	return true
}

func extractContainers(containers []corev1.Container) []ContainerEnv {
	out := make([]ContainerEnv, 0, len(containers))
	for _, c := range containers {
		items := make([]EnvVarItem, 0, len(c.Env))
		for _, ev := range c.Env {
			if ev.ValueFrom != nil {
				items = append(items, EnvVarItem{Name: ev.Name, ReadOnlySource: describeValueFrom(ev.ValueFrom)})
				continue
			}
			val := ev.Value
			items = append(items, EnvVarItem{Name: ev.Name, Value: &val})
		}
		out = append(out, ContainerEnv{Name: c.Name, Env: items})
	}
	return out
}

func describeValueFrom(src *corev1.EnvVarSource) string {
	switch {
	case src == nil:
		return ""
	case src.SecretKeyRef != nil:
		return "secret:" + src.SecretKeyRef.Name + "/" + src.SecretKeyRef.Key
	case src.ConfigMapKeyRef != nil:
		return "configMap:" + src.ConfigMapKeyRef.Name + "/" + src.ConfigMapKeyRef.Key
	case src.FieldRef != nil:
		return "fieldRef:" + src.FieldRef.FieldPath
	case src.ResourceFieldRef != nil:
		return "resourceFieldRef:" + src.ResourceFieldRef.Resource
	default:
		return "valueFrom"
	}
}

// mergeContainerEnv updates plain name/value entries in place, drops omitted plain entries,
// preserves every non-nil ValueFrom entry in its original order (PRD R2), and appends newly added
// plain entries in request order. Touching any ValueFrom variable name is rejected with 400.
func mergeContainerEnv(existing []corev1.EnvVar, desired []PlainEnvVar) ([]corev1.EnvVar, *StatusErr) {
	readOnly := make(map[string]bool)
	for _, ev := range existing {
		if ev.ValueFrom != nil {
			readOnly[ev.Name] = true
		}
	}
	byName := make(map[string]string, len(desired))
	for _, d := range desired {
		if readOnly[d.Name] {
			return nil, &StatusErr{Code: http.StatusBadRequest, Msg: fmt.Sprintf("env var %q uses valueFrom and is read-only", d.Name)}
		}
		byName[d.Name] = *d.Value
	}
	emitted := make(map[string]bool, len(desired))
	var out []corev1.EnvVar
	for _, ev := range existing {
		if ev.ValueFrom != nil {
			out = append(out, *ev.DeepCopy())
			continue
		}
		if newVal, keep := byName[ev.Name]; keep && !emitted[ev.Name] {
			out = append(out, corev1.EnvVar{Name: ev.Name, Value: newVal})
			emitted[ev.Name] = true
		}
	}
	for _, d := range desired {
		if !emitted[d.Name] {
			out = append(out, corev1.EnvVar{Name: d.Name, Value: *d.Value})
			emitted[d.Name] = true
		}
	}
	return out, nil
}
