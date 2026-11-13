package demo

import (
	"context"
	"net/http"

	autoscalingv1 "k8s.io/api/autoscaling/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

// GetWorkloadScale reads replica state and HPA ownership for a Deployment or StatefulSet.
func (w *KubeWorkloads) GetWorkloadScale(ctx context.Context, ref model.Ref) (WorkloadScaleResponse, *StatusErr) {
	if w == nil || w.Client == nil {
		return WorkloadScaleResponse{}, &StatusErr{Code: http.StatusServiceUnavailable, Msg: "kubernetes client unavailable"}
	}
	apps := w.Client.AppsV1()
	switch ref.Kind {
	case "Deployment":
		obj, err := apps.Deployments(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
		if err != nil {
			return WorkloadScaleResponse{}, mapResourceAPIError(ctx, w.Log, "reading deployment scale", "workload", ref, err)
		}
		return buildWorkloadScaleResponse(ref, obj.ResourceVersion, derefReplicas(obj.Spec.Replicas), w.findManagingHPA(ref)), nil
	case "StatefulSet":
		obj, err := apps.StatefulSets(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
		if err != nil {
			return WorkloadScaleResponse{}, mapResourceAPIError(ctx, w.Log, "reading statefulset scale", "workload", ref, err)
		}
		return buildWorkloadScaleResponse(ref, obj.ResourceVersion, derefReplicas(obj.Spec.Replicas), w.findManagingHPA(ref)), nil
	default:
		return WorkloadScaleResponse{}, &StatusErr{Code: http.StatusBadRequest, Msg: "unsupported workload kind"}
	}
}

// PatchWorkloadScale updates spec.replicas (0..50) on a Deployment or StatefulSet.
func (w *KubeWorkloads) PatchWorkloadScale(ctx context.Context, ref model.Ref, req PatchWorkloadScaleRequest) (WorkloadScaleResponse, *StatusErr) {
	if w == nil || w.Client == nil {
		return WorkloadScaleResponse{}, &StatusErr{Code: http.StatusServiceUnavailable, Msg: "kubernetes client unavailable"}
	}
	desired := *req.Replicas
	switch ref.Kind {
	case "Deployment":
		cur, err := w.Client.AppsV1().Deployments(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
		if err != nil {
			return WorkloadScaleResponse{}, mapResourceAPIError(ctx, w.Log, "reading deployment", "workload", ref, err)
		}
		if cur.ResourceVersion != req.ResourceVersion {
			return WorkloadScaleResponse{}, &StatusErr{Code: http.StatusConflict, Msg: resourceConflictMessage}
		}
		next := cur.DeepCopy()
		next.Spec.Replicas = &desired
		updated, err := w.Client.AppsV1().Deployments(ref.Namespace).Update(ctx, next, metav1.UpdateOptions{FieldManager: FieldManagerName})
		if err != nil {
			return WorkloadScaleResponse{}, mapResourceAPIError(ctx, w.Log, "updating deployment replicas", "workload", ref, err)
		}
		return buildWorkloadScaleResponse(ref, updated.ResourceVersion, derefReplicas(updated.Spec.Replicas), w.findManagingHPA(ref)), nil
	case "StatefulSet":
		cur, err := w.Client.AppsV1().StatefulSets(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
		if err != nil {
			return WorkloadScaleResponse{}, mapResourceAPIError(ctx, w.Log, "reading statefulset", "workload", ref, err)
		}
		if cur.ResourceVersion != req.ResourceVersion {
			return WorkloadScaleResponse{}, &StatusErr{Code: http.StatusConflict, Msg: resourceConflictMessage}
		}
		next := cur.DeepCopy()
		next.Spec.Replicas = &desired
		updated, err := w.Client.AppsV1().StatefulSets(ref.Namespace).Update(ctx, next, metav1.UpdateOptions{FieldManager: FieldManagerName})
		if err != nil {
			return WorkloadScaleResponse{}, mapResourceAPIError(ctx, w.Log, "updating statefulset replicas", "workload", ref, err)
		}
		return buildWorkloadScaleResponse(ref, updated.ResourceVersion, derefReplicas(updated.Spec.Replicas), w.findManagingHPA(ref)), nil
	default:
		return WorkloadScaleResponse{}, &StatusErr{Code: http.StatusBadRequest, Msg: "unsupported workload kind"}
	}
}

func derefReplicas(p *int32) int32 {
	if p == nil {
		return 1
	}
	return *p
}

func (w *KubeWorkloads) findManagingHPA(ref model.Ref) string {
	if w == nil || w.Model == nil {
		return ""
	}
	appID := model.AppID(ref.Namespace, ref.Kind, ref.Name)
	ents, _ := w.Model.Snapshot()
	for _, e := range ents {
		if e.Folded || e.Group != "autoscaling" || e.Kind != "HorizontalPodAutoscaler" || e.Namespace != ref.Namespace {
			continue
		}
		if e.AppID == appID {
			return e.Name
		}
		if obj, ok := w.Model.Object(e.ID); ok && hpaTargets(obj, ref.Kind, ref.Name) {
			return e.Name
		}
	}
	return ""
}

func hpaTargets(obj runtime.Object, kind, name string) bool {
	switch o := obj.(type) {
	case *autoscalingv2.HorizontalPodAutoscaler:
		return o.Spec.ScaleTargetRef.Kind == kind && o.Spec.ScaleTargetRef.Name == name
	case *autoscalingv1.HorizontalPodAutoscaler:
		return o.Spec.ScaleTargetRef.Kind == kind && o.Spec.ScaleTargetRef.Name == name
	case *unstructured.Unstructured:
		k, _, _ := unstructured.NestedString(o.Object, "spec", "scaleTargetRef", "kind")
		n, _, _ := unstructured.NestedString(o.Object, "spec", "scaleTargetRef", "name")
		return k == kind && n == name
	default:
		return false
	}
}
