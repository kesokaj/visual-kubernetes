package stream

import (
	"context"
	"fmt"
	"io"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

// ModelLookup is the model's object index (model.Model.Object).
type ModelLookup interface {
	Object(id string) (runtime.Object, bool)
}

// ClusterObjects serves `/api/objects` from the API server. The id must name an object the
// model holds — the model never stores Secrets (R2) — before any request is made; the object
// is then read fresh, because the watch cache is trimmed (ConfigMap data, managedFields).
type ClusterObjects struct {
	Model  ModelLookup
	Dyn    dynamic.Interface
	Mapper meta.RESTMapper
}

// Object implements ObjectSource.
func (c ClusterObjects) Object(ctx context.Context, ref model.Ref) ([]byte, error) {
	if ref.Group == model.CoreGroup && ref.Kind == "Secret" {
		return nil, ErrNotFound // never read, even should one reach the model
	}
	if _, ok := c.Model.Object(ref.ID()); !ok {
		return nil, ErrNotFound
	}
	group := ref.Group
	if group == model.CoreGroup {
		group = ""
	}
	mapping, err := c.Mapper.RESTMapping(schema.GroupKind{Group: group, Kind: ref.Kind})
	if err != nil {
		return nil, fmt.Errorf("mapping %s: %w", ref.ID(), err)
	}
	var res dynamic.ResourceInterface = c.Dyn.Resource(mapping.Resource)
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		res = c.Dyn.Resource(mapping.Resource).Namespace(ref.Namespace)
	}
	u, err := res.Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return nil, apiError(err)
	}
	unstructured.RemoveNestedField(u.Object, "metadata", "managedFields")
	return yaml.Marshal(u.Object)
}

// ClusterLogs serves `/api/logs` through `get pods/log` (plan D9). The pod must be in the
// model; container defaults to the pod's first container and must be one of its containers.
type ClusterLogs struct {
	Model  ModelLookup
	Client kubernetes.Interface
}

// Logs implements LogSource.
func (c ClusterLogs) Logs(ctx context.Context, ref model.Ref, container string, tailLines int64) (io.ReadCloser, error) {
	obj, ok := c.Model.Object(ref.ID())
	if !ok {
		return nil, ErrNotFound
	}
	pod, ok := obj.(*corev1.Pod)
	if !ok || len(pod.Spec.Containers) == 0 {
		return nil, ErrNotFound
	}
	if container == "" {
		container = pod.Spec.Containers[0].Name
	} else if !hasContainer(pod, container) {
		return nil, ErrNotFound
	}
	limit := int64(MaxLogBytes)
	opts := &corev1.PodLogOptions{Container: container, TailLines: &tailLines, LimitBytes: &limit}
	rc, err := c.Client.CoreV1().Pods(ref.Namespace).GetLogs(ref.Name, opts).Stream(ctx)
	if err != nil {
		return nil, apiError(err)
	}
	return rc, nil
}

func hasContainer(pod *corev1.Pod, name string) bool {
	for _, c := range pod.Spec.Containers {
		if c.Name == name {
			return true
		}
	}
	for _, c := range pod.Spec.InitContainers {
		if c.Name == name {
			return true
		}
	}
	for _, c := range pod.Spec.EphemeralContainers {
		if c.Name == name {
			return true
		}
	}
	return false
}

// apiError maps the API server's answer to what the client may see: gone is 404, a denial is
// 403 with a fixed message (the RBAC detail stays in the log), anything else stays an error.
func apiError(err error) error {
	switch {
	case apierrors.IsNotFound(err):
		return ErrNotFound
	case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
		return &StatusError{Code: http.StatusForbidden, Msg: "denied by the cluster's RBAC"}
	default:
		return err
	}
}
