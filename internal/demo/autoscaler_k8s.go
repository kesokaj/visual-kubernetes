package demo

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

var vpaGVR = schema.GroupVersionResource{
	Group:    "autoscaling.k8s.io",
	Version:  "v1",
	Resource: "verticalpodautoscalers",
}

// KubeAutoscalers implements AutoscalerStore against AutoscalingV2 (HPA) and dynamic unstructured (VPA).
type KubeAutoscalers struct {
	Client kubernetes.Interface
	Dyn    dynamic.Interface
	Model  *model.Model
	Log    *slog.Logger
}

// GetAutoscaler reads an HPA or VPA in a non-protected namespace.
func (k *KubeAutoscalers) GetAutoscaler(ctx context.Context, ref model.Ref) (AutoscalerResponse, *StatusErr) {
	if ref.Kind == "HorizontalPodAutoscaler" {
		return k.getHPA(ctx, ref)
	}
	return k.getVPA(ctx, ref)
}

// PatchAutoscaler updates an HPA or VPA with optimistic resourceVersion checking.
func (k *KubeAutoscalers) PatchAutoscaler(ctx context.Context, ref model.Ref, req PatchAutoscalerRequest) (AutoscalerResponse, *StatusErr) {
	if ref.Kind == "HorizontalPodAutoscaler" {
		return k.patchHPA(ctx, ref, req)
	}
	return k.patchVPA(ctx, ref, req)
}

func (k *KubeAutoscalers) getHPA(ctx context.Context, ref model.Ref) (AutoscalerResponse, *StatusErr) {
	if k == nil || k.Client == nil {
		return AutoscalerResponse{}, &StatusErr{Code: http.StatusServiceUnavailable, Msg: "kubernetes client unavailable"}
	}
	hpa, err := k.Client.AutoscalingV2().HorizontalPodAutoscalers(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return AutoscalerResponse{}, mapResourceAPIError(ctx, k.Log, "reading hpa", "autoscaler", ref, err)
	}
	return buildHPAResponse(ref, hpa), nil
}

func (k *KubeAutoscalers) patchHPA(ctx context.Context, ref model.Ref, req PatchAutoscalerRequest) (AutoscalerResponse, *StatusErr) {
	if k == nil || k.Client == nil {
		return AutoscalerResponse{}, &StatusErr{Code: http.StatusServiceUnavailable, Msg: "kubernetes client unavailable"}
	}
	cur, err := k.Client.AutoscalingV2().HorizontalPodAutoscalers(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return AutoscalerResponse{}, mapResourceAPIError(ctx, k.Log, "reading hpa", "autoscaler", ref, err)
	}
	if cur.ResourceVersion != req.ResourceVersion {
		return AutoscalerResponse{}, &StatusErr{Code: http.StatusConflict, Msg: resourceConflictMessage}
	}
	next := cur.DeepCopy()
	minRep := *req.HPA.MinReplicas
	next.Spec.MinReplicas = &minRep
	next.Spec.MaxReplicas = *req.HPA.MaxReplicas
	updated, err := k.Client.AutoscalingV2().HorizontalPodAutoscalers(ref.Namespace).Update(ctx, next, metav1.UpdateOptions{FieldManager: FieldManagerName})
	if err != nil {
		return AutoscalerResponse{}, mapResourceAPIError(ctx, k.Log, "updating hpa", "autoscaler", ref, err)
	}
	return buildHPAResponse(ref, updated), nil
}

func buildHPAResponse(ref model.Ref, hpa *autoscalingv2.HorizontalPodAutoscaler) AutoscalerResponse {
	minRep := int32(1)
	if hpa.Spec.MinReplicas != nil {
		minRep = *hpa.Spec.MinReplicas
	}
	return AutoscalerResponse{
		ID:              ref.ID(),
		Kind:            ref.Kind,
		ResourceVersion: hpa.ResourceVersion,
		HPA: &HPAResponse{
			MinReplicas:     minRep,
			MaxReplicas:     hpa.Spec.MaxReplicas,
			CurrentReplicas: hpa.Status.CurrentReplicas,
		},
	}
}

func (k *KubeAutoscalers) getVPA(ctx context.Context, ref model.Ref) (AutoscalerResponse, *StatusErr) {
	if k == nil || k.Dyn == nil {
		return AutoscalerResponse{}, &StatusErr{Code: http.StatusServiceUnavailable, Msg: "dynamic kubernetes client unavailable"}
	}
	u, err := k.Dyn.Resource(vpaGVR).Namespace(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return AutoscalerResponse{}, mapResourceAPIError(ctx, k.Log, "reading vpa", "autoscaler", ref, err)
	}
	return extractVPAResponse(ref, u, k.Model), nil
}

func (k *KubeAutoscalers) patchVPA(ctx context.Context, ref model.Ref, req PatchAutoscalerRequest) (AutoscalerResponse, *StatusErr) {
	if k == nil || k.Dyn == nil {
		return AutoscalerResponse{}, &StatusErr{Code: http.StatusServiceUnavailable, Msg: "dynamic kubernetes client unavailable"}
	}
	cur, err := k.Dyn.Resource(vpaGVR).Namespace(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return AutoscalerResponse{}, mapResourceAPIError(ctx, k.Log, "reading vpa", "autoscaler", ref, err)
	}
	if cur.GetResourceVersion() != req.ResourceVersion {
		return AutoscalerResponse{}, &StatusErr{Code: http.StatusConflict, Msg: resourceConflictMessage}
	}
	next := cur.DeepCopy()
	if st := applyVPAPatch(next, req.VPA); st != nil {
		return AutoscalerResponse{}, st
	}
	updated, err := k.Dyn.Resource(vpaGVR).Namespace(ref.Namespace).Update(ctx, next, metav1.UpdateOptions{FieldManager: FieldManagerName})
	if err != nil {
		return AutoscalerResponse{}, mapResourceAPIError(ctx, k.Log, "updating vpa", "autoscaler", ref, err)
	}
	return extractVPAResponse(ref, updated, k.Model), nil
}

func extractVPAResponse(ref model.Ref, u *unstructured.Unstructured, m *model.Model) AutoscalerResponse {
	mode, found, _ := unstructured.NestedString(u.Object, "spec", "updatePolicy", "updateMode")
	if !found || strings.TrimSpace(mode) == "" {
		mode = "Auto"
	}
	rawPolicies, _, _ := unstructured.NestedSlice(u.Object, "spec", "resourcePolicy", "containerPolicies")
	containers := make([]VPAContainerPolicy, 0, max(1, len(rawPolicies)))
	for _, raw := range rawPolicies {
		pm, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		cname, _ := pm["containerName"].(string)
		if cname == "" {
			continue
		}
		maxMap, _ := pm["maxAllowed"].(map[string]any)
		containers = append(containers, VPAContainerPolicy{
			ContainerName: cname,
			MaxCPU:        stringifyQuantity(maxMap["cpu"]),
			MaxMemory:     stringifyQuantity(maxMap["memory"]),
		})
	}
	if len(containers) == 0 {
		containers = []VPAContainerPolicy{{ContainerName: firstTargetContainerName(u, m)}}
	}
	return AutoscalerResponse{
		ID:              ref.ID(),
		Kind:            ref.Kind,
		ResourceVersion: u.GetResourceVersion(),
		VPA: &VPAResponse{
			UpdateMode:   mode,
			AllowedModes: allowedVPAModeCopy(),
			Containers:   containers,
		},
	}
}

func applyVPAPatch(next *unstructured.Unstructured, patch *VPAPatchPayload) *StatusErr {
	spec, _ := next.Object["spec"].(map[string]any)
	if spec == nil {
		spec = map[string]any{}
		next.Object["spec"] = spec
	}
	updPol, _ := spec["updatePolicy"].(map[string]any)
	if updPol == nil {
		updPol = map[string]any{}
		spec["updatePolicy"] = updPol
	}
	updPol["updateMode"] = patch.UpdateMode

	resPol, hasResPol := spec["resourcePolicy"].(map[string]any)
	var existingSlice []any
	if resPol != nil {
		existingSlice, _ = resPol["containerPolicies"].([]any)
	}
	merged, st := mergeVPAContainerPolicies(existingSlice, patch.Containers)
	if st != nil {
		return st
	}
	if len(merged) == 0 {
		if hasResPol && resPol != nil {
			delete(resPol, "containerPolicies")
			if len(resPol) == 0 {
				delete(spec, "resourcePolicy")
			}
		}
		return nil
	}
	if resPol == nil {
		resPol = map[string]any{}
		spec["resourcePolicy"] = resPol
	}
	resPol["containerPolicies"] = merged
	return nil
}

func mergeVPAContainerPolicies(existing []any, desired []VPAContainerPolicy) ([]any, *StatusErr) {
	byName := make(map[string]VPAContainerPolicy, len(desired))
	for _, c := range desired {
		byName[c.ContainerName] = c
	}
	seen := make(map[string]bool, len(desired))
	out := make([]any, 0, max(len(existing), len(desired)))
	for _, raw := range existing {
		pol, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		cname, _ := pol["containerName"].(string)
		if d, match := byName[cname]; match && !seen[cname] {
			applyMaxAllowed(pol, d)
			seen[cname] = true
			if len(pol) <= 1 {
				continue
			}
		}
		out = append(out, pol)
	}
	for _, d := range desired {
		if seen[d.ContainerName] || (d.MaxCPU == "" && d.MaxMemory == "") {
			continue
		}
		pol := map[string]any{"containerName": d.ContainerName}
		applyMaxAllowed(pol, d)
		out = append(out, pol)
	}
	if len(out) > maxVPAContainers {
		return nil, &StatusErr{Code: http.StatusBadRequest, Msg: fmt.Sprintf("vpa cannot exceed %d containerPolicies", maxVPAContainers)}
	}
	return out, nil
}

func applyMaxAllowed(pol map[string]any, d VPAContainerPolicy) {
	maxMap, _ := pol["maxAllowed"].(map[string]any)
	if maxMap == nil {
		maxMap = map[string]any{}
	}
	if d.MaxCPU == "" {
		delete(maxMap, "cpu")
	} else {
		maxMap["cpu"] = d.MaxCPU
	}
	if d.MaxMemory == "" {
		delete(maxMap, "memory")
	} else {
		maxMap["memory"] = d.MaxMemory
	}
	if len(maxMap) == 0 {
		delete(pol, "maxAllowed")
	} else {
		pol["maxAllowed"] = maxMap
	}
}

func firstTargetContainerName(u *unstructured.Unstructured, m *model.Model) string {
	if m == nil {
		return "*"
	}
	kind, _, _ := unstructured.NestedString(u.Object, "spec", "targetRef", "kind")
	name, _, _ := unstructured.NestedString(u.Object, "spec", "targetRef", "name")
	if kind == "" || name == "" {
		return "*"
	}
	obj, ok := m.Object(model.ID("apps", kind, u.GetNamespace(), name))
	if !ok {
		return "*"
	}
	if cname := firstWorkloadContainer(obj); cname != "" {
		return cname
	}
	return "*"
}

func firstWorkloadContainer(obj runtime.Object) string {
	switch w := obj.(type) {
	case *appsv1.Deployment:
		if len(w.Spec.Template.Spec.Containers) > 0 {
			return w.Spec.Template.Spec.Containers[0].Name
		}
	case *appsv1.StatefulSet:
		if len(w.Spec.Template.Spec.Containers) > 0 {
			return w.Spec.Template.Spec.Containers[0].Name
		}
	case *appsv1.DaemonSet:
		if len(w.Spec.Template.Spec.Containers) > 0 {
			return w.Spec.Template.Spec.Containers[0].Name
		}
	case *unstructured.Unstructured:
		cs, _, _ := unstructured.NestedSlice(w.Object, "spec", "template", "spec", "containers")
		if len(cs) > 0 {
			if c0, ok := cs[0].(map[string]any); ok {
				name, _ := c0["name"].(string)
				return name
			}
		}
	}
	return ""
}

func stringifyQuantity(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case int:
		return strconv.Itoa(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return fmt.Sprint(x)
	}
}
