package demo

import (
	"context"
	"log/slog"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

const conflictMessage = "workload changed since it was loaded; reload and try again"

// KubeWorkloads implements WorkloadStore against the live Kubernetes AppsV1 API without reading
// any Secret objects (PRD R1, R2).
type KubeWorkloads struct {
	Client kubernetes.Interface
	Log    *slog.Logger
}

// GetWorkloadEnv reads container env metadata from a Deployment, StatefulSet, or DaemonSet.
func (w *KubeWorkloads) GetWorkloadEnv(ctx context.Context, ref model.Ref) (WorkloadEnvResponse, *StatusErr) {
	if w == nil || w.Client == nil {
		return WorkloadEnvResponse{}, &StatusErr{Code: http.StatusServiceUnavailable, Msg: "kubernetes client unavailable"}
	}
	rv, containers, err := w.fetchTemplate(ctx, ref)
	if err != nil {
		return WorkloadEnvResponse{}, w.mapAPIError(ctx, "reading workload env", ref, err)
	}
	return WorkloadEnvResponse{
		ID:              ref.ID(),
		ResourceVersion: rv,
		Containers:      extractContainers(containers),
	}, nil
}

// PatchWorkloadEnv updates plain container env vars on a Deployment, StatefulSet, or DaemonSet
// with optimistic resourceVersion checking and FieldManager "visual-kubernetes-demo".
func (w *KubeWorkloads) PatchWorkloadEnv(ctx context.Context, ref model.Ref, req PatchWorkloadEnvRequest) (WorkloadEnvResponse, *StatusErr) {
	if w == nil || w.Client == nil {
		return WorkloadEnvResponse{}, &StatusErr{Code: http.StatusServiceUnavailable, Msg: "kubernetes client unavailable"}
	}
	switch ref.Kind {
	case "Deployment":
		return w.patchDeployment(ctx, ref, req)
	case "StatefulSet":
		return w.patchStatefulSet(ctx, ref, req)
	case "DaemonSet":
		return w.patchDaemonSet(ctx, ref, req)
	default:
		return WorkloadEnvResponse{}, &StatusErr{Code: http.StatusBadRequest, Msg: "unsupported workload kind"}
	}
}

func (w *KubeWorkloads) fetchTemplate(ctx context.Context, ref model.Ref) (string, []corev1.Container, error) {
	apps := w.Client.AppsV1()
	switch ref.Kind {
	case "Deployment":
		obj, err := apps.Deployments(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
		if err != nil {
			return "", nil, err
		}
		return obj.ResourceVersion, obj.Spec.Template.Spec.Containers, nil
	case "StatefulSet":
		obj, err := apps.StatefulSets(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
		if err != nil {
			return "", nil, err
		}
		return obj.ResourceVersion, obj.Spec.Template.Spec.Containers, nil
	default:
		obj, err := apps.DaemonSets(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
		if err != nil {
			return "", nil, err
		}
		return obj.ResourceVersion, obj.Spec.Template.Spec.Containers, nil
	}
}

func (w *KubeWorkloads) patchDeployment(ctx context.Context, ref model.Ref, req PatchWorkloadEnvRequest) (WorkloadEnvResponse, *StatusErr) {
	cur, err := w.Client.AppsV1().Deployments(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return WorkloadEnvResponse{}, w.mapAPIError(ctx, "reading deployment", ref, err)
	}
	if cur.ResourceVersion != req.ResourceVersion {
		return WorkloadEnvResponse{}, &StatusErr{Code: http.StatusConflict, Msg: conflictMessage}
	}
	next := cur.DeepCopy()
	if st := applyContainerPatch(next.Spec.Template.Spec.Containers, req); st != nil {
		return WorkloadEnvResponse{}, st
	}
	updated, err := w.Client.AppsV1().Deployments(ref.Namespace).Update(ctx, next, metav1.UpdateOptions{FieldManager: FieldManagerName})
	if err != nil {
		return WorkloadEnvResponse{}, w.mapAPIError(ctx, "updating deployment env", ref, err)
	}
	return WorkloadEnvResponse{
		ID:              ref.ID(),
		ResourceVersion: updated.ResourceVersion,
		Containers:      extractContainers(updated.Spec.Template.Spec.Containers),
	}, nil
}

func (w *KubeWorkloads) patchStatefulSet(ctx context.Context, ref model.Ref, req PatchWorkloadEnvRequest) (WorkloadEnvResponse, *StatusErr) {
	cur, err := w.Client.AppsV1().StatefulSets(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return WorkloadEnvResponse{}, w.mapAPIError(ctx, "reading statefulset", ref, err)
	}
	if cur.ResourceVersion != req.ResourceVersion {
		return WorkloadEnvResponse{}, &StatusErr{Code: http.StatusConflict, Msg: conflictMessage}
	}
	next := cur.DeepCopy()
	if st := applyContainerPatch(next.Spec.Template.Spec.Containers, req); st != nil {
		return WorkloadEnvResponse{}, st
	}
	updated, err := w.Client.AppsV1().StatefulSets(ref.Namespace).Update(ctx, next, metav1.UpdateOptions{FieldManager: FieldManagerName})
	if err != nil {
		return WorkloadEnvResponse{}, w.mapAPIError(ctx, "updating statefulset env", ref, err)
	}
	return WorkloadEnvResponse{
		ID:              ref.ID(),
		ResourceVersion: updated.ResourceVersion,
		Containers:      extractContainers(updated.Spec.Template.Spec.Containers),
	}, nil
}

func (w *KubeWorkloads) patchDaemonSet(ctx context.Context, ref model.Ref, req PatchWorkloadEnvRequest) (WorkloadEnvResponse, *StatusErr) {
	cur, err := w.Client.AppsV1().DaemonSets(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return WorkloadEnvResponse{}, w.mapAPIError(ctx, "reading daemonset", ref, err)
	}
	if cur.ResourceVersion != req.ResourceVersion {
		return WorkloadEnvResponse{}, &StatusErr{Code: http.StatusConflict, Msg: conflictMessage}
	}
	next := cur.DeepCopy()
	if st := applyContainerPatch(next.Spec.Template.Spec.Containers, req); st != nil {
		return WorkloadEnvResponse{}, st
	}
	updated, err := w.Client.AppsV1().DaemonSets(ref.Namespace).Update(ctx, next, metav1.UpdateOptions{FieldManager: FieldManagerName})
	if err != nil {
		return WorkloadEnvResponse{}, w.mapAPIError(ctx, "updating daemonset env", ref, err)
	}
	return WorkloadEnvResponse{
		ID:              ref.ID(),
		ResourceVersion: updated.ResourceVersion,
		Containers:      extractContainers(updated.Spec.Template.Spec.Containers),
	}, nil
}

func applyContainerPatch(containers []corev1.Container, req PatchWorkloadEnvRequest) *StatusErr {
	for i := range containers {
		if containers[i].Name != req.Container {
			continue
		}
		merged, st := mergeContainerEnv(containers[i].Env, req.Env)
		if st != nil {
			return st
		}
		containers[i].Env = merged
		return nil
	}
	return &StatusErr{Code: http.StatusBadRequest, Msg: "unknown container"}
}

func (w *KubeWorkloads) mapAPIError(ctx context.Context, op string, ref model.Ref, err error) *StatusErr {
	switch {
	case apierrors.IsConflict(err):
		return &StatusErr{Code: http.StatusConflict, Msg: conflictMessage}
	case apierrors.IsNotFound(err):
		return &StatusErr{Code: http.StatusNotFound, Msg: "workload not found"}
	case apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err):
		return &StatusErr{Code: http.StatusForbidden, Msg: "forbidden by cluster RBAC"}
	case apierrors.IsInvalid(err) || apierrors.IsBadRequest(err):
		return &StatusErr{Code: http.StatusBadRequest, Msg: "cluster rejected workload env update"}
	default:
		if w.Log != nil {
			w.Log.WarnContext(ctx, op+" failed", "id", ref.ID(), "err", err)
		}
		return &StatusErr{Code: http.StatusBadGateway, Msg: "kubernetes API error"}
	}
}
