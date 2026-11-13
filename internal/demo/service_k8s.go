package demo

import (
	"context"
	"log/slog"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

// KubeServices implements ServiceStore against the live Kubernetes CoreV1 API (ADR-013).
type KubeServices struct {
	Client kubernetes.Interface
	Log    *slog.Logger
}

// GetServiceType reads a non-protected Service's exposure type.
func (k *KubeServices) GetServiceType(ctx context.Context, ref model.Ref) (ServiceTypeResponse, *StatusErr) {
	if k == nil || k.Client == nil {
		return ServiceTypeResponse{}, &StatusErr{Code: http.StatusServiceUnavailable, Msg: "kubernetes client unavailable"}
	}
	svc, err := k.Client.CoreV1().Services(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return ServiceTypeResponse{}, mapResourceAPIError(ctx, k.Log, "reading service", "service", ref, err)
	}
	if st := validateEditableService(svc); st != nil {
		return ServiceTypeResponse{}, st
	}
	return buildServiceTypeResponse(ref, svc), nil
}

// PatchServiceType switches a non-protected Service between ClusterIP and LoadBalancer.
func (k *KubeServices) PatchServiceType(ctx context.Context, ref model.Ref, req PatchServiceTypeRequest) (ServiceTypeResponse, *StatusErr) {
	if k == nil || k.Client == nil {
		return ServiceTypeResponse{}, &StatusErr{Code: http.StatusServiceUnavailable, Msg: "kubernetes client unavailable"}
	}
	cur, err := k.Client.CoreV1().Services(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return ServiceTypeResponse{}, mapResourceAPIError(ctx, k.Log, "reading service", "service", ref, err)
	}
	if st := validateEditableService(cur); st != nil {
		return ServiceTypeResponse{}, st
	}
	if cur.ResourceVersion != req.ResourceVersion {
		return ServiceTypeResponse{}, &StatusErr{Code: http.StatusConflict, Msg: resourceConflictMessage}
	}
	next := cur.DeepCopy()
	applyServiceTypeSpec(&next.Spec, corev1.ServiceType(req.Type))
	updated, err := k.Client.CoreV1().Services(ref.Namespace).Update(ctx, next, metav1.UpdateOptions{FieldManager: FieldManagerName})
	if err != nil {
		return ServiceTypeResponse{}, mapResourceAPIError(ctx, k.Log, "updating service type", "service", ref, err)
	}
	return buildServiceTypeResponse(ref, updated), nil
}

func validateEditableService(svc *corev1.Service) *StatusErr {
	if svc.Spec.Type == corev1.ServiceTypeExternalName {
		return &StatusErr{Code: http.StatusBadRequest, Msg: "ExternalName Service cannot switch type"}
	}
	if svc.Spec.ClusterIP == corev1.ClusterIPNone {
		return &StatusErr{Code: http.StatusBadRequest, Msg: "headless Service (clusterIP: None) cannot switch type"}
	}
	if len(svc.Spec.Selector) == 0 {
		return &StatusErr{Code: http.StatusBadRequest, Msg: "Service without pod selector cannot switch type"}
	}
	return nil
}

// applyServiceTypeSpec updates spec.type and, when reverting to ClusterIP, clears LoadBalancer/NodePort
// fields that kube-apiserver rejects on ClusterIP Services (D14).
func applyServiceTypeSpec(spec *corev1.ServiceSpec, target corev1.ServiceType) {
	spec.Type = target
	if target != corev1.ServiceTypeClusterIP {
		return
	}
	for i := range spec.Ports {
		spec.Ports[i].NodePort = 0
	}
	spec.HealthCheckNodePort = 0
	spec.AllocateLoadBalancerNodePorts = nil
	spec.LoadBalancerClass = nil
	if len(spec.ExternalIPs) == 0 {
		spec.ExternalTrafficPolicy = ""
	} else if spec.ExternalTrafficPolicy == corev1.ServiceExternalTrafficPolicyLocal {
		spec.ExternalTrafficPolicy = corev1.ServiceExternalTrafficPolicyCluster
	}
}

func buildServiceTypeResponse(ref model.Ref, svc *corev1.Service) ServiceTypeResponse {
	svcType := string(svc.Spec.Type)
	if svcType == "" {
		svcType = string(corev1.ServiceTypeClusterIP)
	}
	return ServiceTypeResponse{
		ID:              ref.ID(),
		ResourceVersion: svc.ResourceVersion,
		Type:            svcType,
		AllowedTypes:    allowedServiceTypes(),
		ClusterIP:       svc.Spec.ClusterIP,
		Headless:        svc.Spec.ClusterIP == corev1.ClusterIPNone,
	}
}

func mapResourceAPIError(ctx context.Context, log *slog.Logger, op, noun string, ref model.Ref, err error) *StatusErr {
	switch {
	case apierrors.IsConflict(err):
		return &StatusErr{Code: http.StatusConflict, Msg: resourceConflictMessage}
	case apierrors.IsNotFound(err) || meta.IsNoMatchError(err):
		return &StatusErr{Code: http.StatusNotFound, Msg: noun + " not found"}
	case apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err):
		return &StatusErr{Code: http.StatusForbidden, Msg: "forbidden by cluster RBAC"}
	case apierrors.IsInvalid(err) || apierrors.IsBadRequest(err):
		return &StatusErr{Code: http.StatusBadRequest, Msg: "cluster rejected " + noun + " update"}
	default:
		if log != nil {
			log.WarnContext(ctx, op+" failed", "id", ref.ID(), "err", err)
		}
		return &StatusErr{Code: http.StatusBadGateway, Msg: "kubernetes API error"}
	}
}
