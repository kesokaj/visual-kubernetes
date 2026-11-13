package demo

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

const (
	minWorkloadReplicas = 0
	maxWorkloadReplicas = 50
)

// WorkloadScaleResponse is the JSON payload returned by GET and PATCH /api/demo/workloads/scale.
type WorkloadScaleResponse struct {
	ID              string `json:"id"`
	Kind            string `json:"kind"`
	ResourceVersion string `json:"resourceVersion"`
	Replicas        int32  `json:"replicas"`
	MinReplicas     int32  `json:"minReplicas"`
	MaxReplicas     int32  `json:"maxReplicas"`
	ManagedByHPA    string `json:"managedByHPA"`
}

// PatchWorkloadScaleRequest is the JSON request body for PATCH /api/demo/workloads/scale.
type PatchWorkloadScaleRequest struct {
	ID              string `json:"id"`
	ResourceVersion string `json:"resourceVersion"`
	Replicas        *int32 `json:"replicas"`
}

func (s *Server) handleGetWorkloadScale(w http.ResponseWriter, r *http.Request) {
	if s.workloads == nil {
		writePlain(w, http.StatusServiceUnavailable, "workload scale editor unavailable")
		return
	}
	ref, st := parseScalableWorkloadRef(r.URL.Query().Get("id"))
	if st != nil {
		writePlain(w, st.Code, st.Msg)
		return
	}
	resp, st := s.workloads.GetWorkloadScale(r.Context(), ref)
	if st != nil {
		writePlain(w, st.Code, st.Msg)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePatchWorkloadScale(w http.ResponseWriter, r *http.Request) {
	if s.workloads == nil {
		writePlain(w, http.StatusServiceUnavailable, "workload scale editor unavailable")
		return
	}
	var req PatchWorkloadScaleRequest
	if !decodeJSONBody(w, r, maxDemoPatchBytes, &req, "invalid workload scale payload") {
		return
	}
	ref, st := validatePatchWorkloadScaleRequest(req)
	if st != nil {
		writePlain(w, st.Code, st.Msg)
		return
	}
	resp, st := s.workloads.PatchWorkloadScale(r.Context(), ref, req)
	if st != nil {
		writePlain(w, st.Code, st.Msg)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func parseScalableWorkloadRef(rawID string) (model.Ref, *StatusErr) {
	ref, err := model.ParseID(strings.TrimSpace(rawID))
	if err != nil {
		return model.Ref{}, &StatusErr{Code: http.StatusBadRequest, Msg: "malformed workload id"}
	}
	if ref.Group != "apps" {
		return model.Ref{}, &StatusErr{Code: http.StatusBadRequest, Msg: "unsupported workload kind"}
	}
	if IsProtectedNamespace(ref.Namespace) {
		return model.Ref{}, &StatusErr{Code: http.StatusForbidden, Msg: "protected namespace is refused in demo mode"}
	}
	if st := validateKubeObjectRef(ref); st != nil {
		return model.Ref{}, st
	}
	if ref.Kind == "DaemonSet" {
		return model.Ref{}, &StatusErr{Code: http.StatusBadRequest, Msg: "DaemonSet runs one pod per node and cannot be scaled by replica count"}
	}
	if ref.Kind != "Deployment" && ref.Kind != "StatefulSet" {
		return model.Ref{}, &StatusErr{Code: http.StatusBadRequest, Msg: "unsupported workload kind"}
	}
	return ref, nil
}

func validatePatchWorkloadScaleRequest(req PatchWorkloadScaleRequest) (model.Ref, *StatusErr) {
	ref, st := parseScalableWorkloadRef(req.ID)
	if st != nil {
		return model.Ref{}, st
	}
	if strings.TrimSpace(req.ResourceVersion) == "" {
		return model.Ref{}, &StatusErr{Code: http.StatusBadRequest, Msg: "resourceVersion is required"}
	}
	if req.Replicas == nil {
		return model.Ref{}, &StatusErr{Code: http.StatusBadRequest, Msg: "replicas is required"}
	}
	if *req.Replicas < minWorkloadReplicas || *req.Replicas > maxWorkloadReplicas {
		return model.Ref{}, &StatusErr{
			Code: http.StatusBadRequest,
			Msg:  fmt.Sprintf("replicas must be between %d and %d", minWorkloadReplicas, maxWorkloadReplicas),
		}
	}
	return ref, nil
}

func buildWorkloadScaleResponse(ref model.Ref, rv string, replicas int32, managedByHPA string) WorkloadScaleResponse {
	return WorkloadScaleResponse{
		ID:              ref.ID(),
		Kind:            ref.Kind,
		ResourceVersion: rv,
		Replicas:        replicas,
		MinReplicas:     minWorkloadReplicas,
		MaxReplicas:     maxWorkloadReplicas,
		ManagedByHPA:    managedByHPA,
	}
}
