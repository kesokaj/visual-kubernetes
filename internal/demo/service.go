package demo

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	corev1 "k8s.io/api/core/v1"
	pathvalidation "k8s.io/apimachinery/pkg/api/validation/path"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

const (
	resourceConflictMessage = "resource changed since it was loaded; reload and try again"
	maxDemoPatchBytes       = 64 << 10
)

// ServiceTypeResponse is the JSON payload returned by GET and PATCH /api/demo/services/type.
type ServiceTypeResponse struct {
	ID              string   `json:"id"`
	ResourceVersion string   `json:"resourceVersion"`
	Type            string   `json:"type"`
	AllowedTypes    []string `json:"allowedTypes"`
	ClusterIP       string   `json:"clusterIP"`
	Headless        bool     `json:"headless"`
}

// PatchServiceTypeRequest is the JSON request body for PATCH /api/demo/services/type.
type PatchServiceTypeRequest struct {
	ID              string `json:"id"`
	ResourceVersion string `json:"resourceVersion"`
	Type            string `json:"type"`
}

// ServiceStore reads and updates Service exposure types (ClusterIP <-> LoadBalancer) in --demo mode.
type ServiceStore interface {
	GetServiceType(ctx context.Context, ref model.Ref) (ServiceTypeResponse, *StatusErr)
	PatchServiceType(ctx context.Context, ref model.Ref, req PatchServiceTypeRequest) (ServiceTypeResponse, *StatusErr)
}

func (s *Server) handleGetServiceType(w http.ResponseWriter, r *http.Request) {
	if s.services == nil {
		writePlain(w, http.StatusServiceUnavailable, "service editor unavailable")
		return
	}
	ref, st := parseServiceRef(r.URL.Query().Get("id"))
	if st != nil {
		writePlain(w, st.Code, st.Msg)
		return
	}
	resp, st := s.services.GetServiceType(r.Context(), ref)
	if st != nil {
		writePlain(w, st.Code, st.Msg)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePatchServiceType(w http.ResponseWriter, r *http.Request) {
	if s.services == nil {
		writePlain(w, http.StatusServiceUnavailable, "service editor unavailable")
		return
	}
	var req PatchServiceTypeRequest
	if !decodeJSONBody(w, r, maxDemoPatchBytes, &req, "invalid service type payload") {
		return
	}
	ref, st := validatePatchServiceTypeRequest(req)
	if st != nil {
		writePlain(w, st.Code, st.Msg)
		return
	}
	resp, st := s.services.PatchServiceType(r.Context(), ref, req)
	if st != nil {
		writePlain(w, st.Code, st.Msg)
		return
	}
	if s.catalog != nil {
		s.catalog.SetServiceTypeOverride(ref.Namespace, ref.Name, corev1.ServiceType(resp.Type), resp.ResourceVersion)
		if resp.Type == string(corev1.ServiceTypeClusterIP) && !s.catalog.IsServiceRouted(ref.Namespace, ref.Name) {
			s.gate.revokeService(ref.Namespace, ref.Name)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, maxBytes int64, dst any, errMsg string) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writePlain(w, http.StatusBadRequest, errMsg)
		return false
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writePlain(w, http.StatusBadRequest, errMsg)
		return false
	}
	return true
}

func parseServiceRef(rawID string) (model.Ref, *StatusErr) {
	ref, err := model.ParseID(strings.TrimSpace(rawID))
	if err != nil {
		return model.Ref{}, &StatusErr{Code: http.StatusBadRequest, Msg: "malformed service id"}
	}
	if ref.Group != "core" || ref.Kind != "Service" {
		return model.Ref{}, &StatusErr{Code: http.StatusBadRequest, Msg: "unsupported service kind"}
	}
	if IsProtectedNamespace(ref.Namespace) {
		return model.Ref{}, &StatusErr{Code: http.StatusForbidden, Msg: "protected namespace is refused in demo mode"}
	}
	if st := validateKubeObjectRef(ref); st != nil {
		return model.Ref{}, st
	}
	if ref.Namespace == "default" && ref.Name == "kubernetes" {
		return model.Ref{}, &StatusErr{Code: http.StatusForbidden, Msg: "default/kubernetes API service cannot be modified"}
	}
	return ref, nil
}

func validateKubeObjectRef(ref model.Ref) *StatusErr {
	if len(validation.IsDNS1123Label(ref.Namespace)) != 0 {
		return &StatusErr{Code: http.StatusBadRequest, Msg: "invalid namespace name"}
	}
	if len(ref.Name) > validation.DNS1123SubdomainMaxLength || len(pathvalidation.ValidatePathSegmentName(ref.Name, false)) != 0 {
		return &StatusErr{Code: http.StatusBadRequest, Msg: "invalid resource name"}
	}
	return nil
}

func validatePatchServiceTypeRequest(req PatchServiceTypeRequest) (model.Ref, *StatusErr) {
	ref, st := parseServiceRef(req.ID)
	if st != nil {
		return model.Ref{}, st
	}
	if strings.TrimSpace(req.ResourceVersion) == "" {
		return model.Ref{}, &StatusErr{Code: http.StatusBadRequest, Msg: "resourceVersion is required"}
	}
	if req.Type != string(corev1.ServiceTypeClusterIP) && req.Type != string(corev1.ServiceTypeLoadBalancer) {
		return model.Ref{}, &StatusErr{Code: http.StatusBadRequest, Msg: "service type must be ClusterIP or LoadBalancer"}
	}
	return ref, nil
}

func allowedServiceTypes() []string {
	return []string{string(corev1.ServiceTypeClusterIP), string(corev1.ServiceTypeLoadBalancer)}
}
