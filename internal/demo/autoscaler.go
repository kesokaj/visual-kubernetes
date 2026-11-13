package demo

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

const (
	minHPAReplicas   = 1
	maxHPAReplicas   = 50
	maxVPAContainers = 32
)

var (
	vpaAllowedModes = []string{"Off", "Initial", "Recreate", "Auto", "InPlaceOrRecreate"}
	maxVPACPU       = resource.MustParse("64")
	maxVPAMemory    = resource.MustParse("128Gi")
)

// AutoscalerResponse is the JSON payload returned by GET and PATCH /api/demo/autoscalers.
type AutoscalerResponse struct {
	ID              string       `json:"id"`
	Kind            string       `json:"kind"`
	ResourceVersion string       `json:"resourceVersion"`
	HPA             *HPAResponse `json:"hpa,omitempty"`
	VPA             *VPAResponse `json:"vpa,omitempty"`
}

// HPAResponse describes HorizontalPodAutoscaler replica bounds.
type HPAResponse struct {
	MinReplicas     int32 `json:"minReplicas"`
	MaxReplicas     int32 `json:"maxReplicas"`
	CurrentReplicas int32 `json:"currentReplicas"`
}

// VPAResponse describes VerticalPodAutoscaler update policy and container maxAllowed ceilings.
type VPAResponse struct {
	UpdateMode   string               `json:"updateMode"`
	AllowedModes []string             `json:"allowedModes"`
	Containers   []VPAContainerPolicy `json:"containers"`
}

// VPAContainerPolicy holds per-container maxAllowed CPU and memory limits on a VPA.
type VPAContainerPolicy struct {
	ContainerName string `json:"containerName"`
	MaxCPU        string `json:"maxCPU"`
	MaxMemory     string `json:"maxMemory"`
}

// PatchAutoscalerRequest is the JSON request body for PATCH /api/demo/autoscalers.
type PatchAutoscalerRequest struct {
	ID              string           `json:"id"`
	ResourceVersion string           `json:"resourceVersion"`
	HPA             *HPAPatchPayload `json:"hpa,omitempty"`
	VPA             *VPAPatchPayload `json:"vpa,omitempty"`
}

// HPAPatchPayload updates minReplicas and maxReplicas on a HorizontalPodAutoscaler.
type HPAPatchPayload struct {
	MinReplicas *int32 `json:"minReplicas"`
	MaxReplicas *int32 `json:"maxReplicas"`
}

// VPAPatchPayload updates updateMode and per-container maxAllowed on a VerticalPodAutoscaler.
type VPAPatchPayload struct {
	UpdateMode string               `json:"updateMode"`
	Containers []VPAContainerPolicy `json:"containers"`
}

// AutoscalerStore reads and updates HPA and VPA objects in --demo mode.
type AutoscalerStore interface {
	GetAutoscaler(ctx context.Context, ref model.Ref) (AutoscalerResponse, *StatusErr)
	PatchAutoscaler(ctx context.Context, ref model.Ref, req PatchAutoscalerRequest) (AutoscalerResponse, *StatusErr)
}

func (s *Server) handleGetAutoscaler(w http.ResponseWriter, r *http.Request) {
	if s.autoscalers == nil {
		writePlain(w, http.StatusServiceUnavailable, "autoscaler editor unavailable")
		return
	}
	ref, st := parseAutoscalerRef(r.URL.Query().Get("id"))
	if st != nil {
		writePlain(w, st.Code, st.Msg)
		return
	}
	resp, st := s.autoscalers.GetAutoscaler(r.Context(), ref)
	if st != nil {
		writePlain(w, st.Code, st.Msg)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePatchAutoscaler(w http.ResponseWriter, r *http.Request) {
	if s.autoscalers == nil {
		writePlain(w, http.StatusServiceUnavailable, "autoscaler editor unavailable")
		return
	}
	var req PatchAutoscalerRequest
	if !decodeJSONBody(w, r, maxDemoPatchBytes, &req, "invalid autoscaler payload") {
		return
	}
	ref, st := validatePatchAutoscalerRequest(&req)
	if st != nil {
		writePlain(w, st.Code, st.Msg)
		return
	}
	resp, st := s.autoscalers.PatchAutoscaler(r.Context(), ref, req)
	if st != nil {
		writePlain(w, st.Code, st.Msg)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func parseAutoscalerRef(rawID string) (model.Ref, *StatusErr) {
	ref, err := model.ParseID(strings.TrimSpace(rawID))
	if err != nil {
		return model.Ref{}, &StatusErr{Code: http.StatusBadRequest, Msg: "malformed autoscaler id"}
	}
	isHPA := ref.Group == "autoscaling" && ref.Kind == "HorizontalPodAutoscaler"
	isVPA := ref.Group == "autoscaling.k8s.io" && ref.Kind == "VerticalPodAutoscaler"
	if !isHPA && !isVPA {
		return model.Ref{}, &StatusErr{Code: http.StatusBadRequest, Msg: "unsupported autoscaler kind"}
	}
	if IsProtectedNamespace(ref.Namespace) {
		return model.Ref{}, &StatusErr{Code: http.StatusForbidden, Msg: "protected namespace is refused in demo mode"}
	}
	if st := validateKubeObjectRef(ref); st != nil {
		return model.Ref{}, st
	}
	return ref, nil
}

func validatePatchAutoscalerRequest(req *PatchAutoscalerRequest) (model.Ref, *StatusErr) {
	ref, st := parseAutoscalerRef(req.ID)
	if st != nil {
		return model.Ref{}, st
	}
	if strings.TrimSpace(req.ResourceVersion) == "" {
		return model.Ref{}, &StatusErr{Code: http.StatusBadRequest, Msg: "resourceVersion is required"}
	}
	if ref.Kind == "HorizontalPodAutoscaler" {
		return ref, validateHPAPatch(req)
	}
	return ref, validateVPAPatch(req)
}

func validateHPAPatch(req *PatchAutoscalerRequest) *StatusErr {
	if req.HPA == nil || req.VPA != nil || req.HPA.MinReplicas == nil || req.HPA.MaxReplicas == nil {
		return &StatusErr{Code: http.StatusBadRequest, Msg: "hpa minReplicas and maxReplicas are required"}
	}
	lo, hi := *req.HPA.MinReplicas, *req.HPA.MaxReplicas
	if lo < minHPAReplicas || lo > maxHPAReplicas {
		return &StatusErr{Code: http.StatusBadRequest, Msg: fmt.Sprintf("minReplicas must be between %d and %d", minHPAReplicas, maxHPAReplicas)}
	}
	if hi < lo || hi > maxHPAReplicas {
		return &StatusErr{Code: http.StatusBadRequest, Msg: fmt.Sprintf("maxReplicas must be between minReplicas and %d", maxHPAReplicas)}
	}
	return nil
}

func validateVPAPatch(req *PatchAutoscalerRequest) *StatusErr {
	if req.VPA == nil || req.HPA != nil {
		return &StatusErr{Code: http.StatusBadRequest, Msg: "vpa payload is required"}
	}
	if !slices.Contains(vpaAllowedModes, req.VPA.UpdateMode) {
		return &StatusErr{Code: http.StatusBadRequest, Msg: "unsupported VPA updateMode"}
	}
	if req.VPA.Containers == nil || len(req.VPA.Containers) > maxVPAContainers {
		return &StatusErr{Code: http.StatusBadRequest, Msg: "vpa containers array is required (max 32)"}
	}
	seen := make(map[string]bool, len(req.VPA.Containers))
	for i := range req.VPA.Containers {
		c := &req.VPA.Containers[i]
		c.ContainerName = strings.TrimSpace(c.ContainerName)
		c.MaxCPU = strings.TrimSpace(c.MaxCPU)
		c.MaxMemory = strings.TrimSpace(c.MaxMemory)
		if c.ContainerName == "" || seen[c.ContainerName] {
			return &StatusErr{Code: http.StatusBadRequest, Msg: "vpa containerName must be non-empty and unique"}
		}
		if c.ContainerName != "*" && len(validation.IsDNS1123Label(c.ContainerName)) != 0 {
			return &StatusErr{Code: http.StatusBadRequest, Msg: "vpa containerName must be '*' or a valid DNS-1123 label"}
		}
		seen[c.ContainerName] = true
		if st := validateVPAResourceLimits(c.MaxCPU, c.MaxMemory); st != nil {
			return st
		}
	}
	return nil
}

func validateVPAResourceLimits(cpuRaw, memRaw string) *StatusErr {
	if cpuRaw != "" {
		q, err := resource.ParseQuantity(cpuRaw)
		if err != nil || q.Sign() <= 0 || q.Cmp(maxVPACPU) > 0 {
			return &StatusErr{Code: http.StatusBadRequest, Msg: "maxCPU must be a positive Kubernetes quantity <= 64"}
		}
	}
	if memRaw != "" {
		q, err := resource.ParseQuantity(memRaw)
		if err != nil || q.CmpInt64(1) < 0 || q.Cmp(maxVPAMemory) > 0 {
			return &StatusErr{Code: http.StatusBadRequest, Msg: "maxMemory must be a positive Kubernetes quantity <= 128Gi"}
		}
	}
	return nil
}

func allowedVPAModeCopy() []string {
	return slices.Clone(vpaAllowedModes)
}
