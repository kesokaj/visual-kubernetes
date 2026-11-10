package stream

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

// mapModel is a ModelLookup over a map.
type mapModel map[string]runtime.Object

func (m mapModel) Object(id string) (runtime.Object, bool) { o, ok := m[id]; return o, ok }

func clusterPod() *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": "web-1", "namespace": "demo",
			"managedFields": []any{map[string]any{"manager": "kubectl"}}},
		"spec": map[string]any{"containers": []any{map[string]any{"name": "app", "image": "nginx"}}},
	}}
	return u
}

func clusterObjects(t *testing.T, inModel mapModel) (ClusterObjects, *dynamicfake.FakeDynamicClient) {
	t.Helper()
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{gvr: "PodList", {Version: "v1", Resource: "secrets"}: "SecretList"}, clusterPod())
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{{Version: "v1"}})
	mapper.Add(schema.GroupVersionKind{Version: "v1", Kind: "Pod"}, meta.RESTScopeNamespace)
	mapper.Add(schema.GroupVersionKind{Version: "v1", Kind: "Secret"}, meta.RESTScopeNamespace)
	return ClusterObjects{Model: inModel, Dyn: dyn, Mapper: mapper}, dyn
}

func TestClusterObjectsStripsManagedFields(t *testing.T) {
	co, _ := clusterObjects(t, mapModel{"core/Pod/demo/web-1": &corev1.Pod{}})
	out, err := co.Object(t.Context(), model.Ref{Group: "core", Kind: "Pod", Namespace: "demo", Name: "web-1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "name: web-1") || strings.Contains(string(out), "managedFields") {
		t.Errorf("YAML = %q, want the pod without managedFields", out)
	}
}

// TestClusterObjectsModelFirst: ids not in the model, and Secrets even when present, never
// reach the API server.
func TestClusterObjectsModelFirst(t *testing.T) {
	co, dyn := clusterObjects(t, mapModel{"core/Secret/demo/token": &corev1.Secret{}})
	for _, ref := range []model.Ref{
		{Group: "core", Kind: "Pod", Namespace: "demo", Name: "web-1"}, // in the cluster, not the model
		{Group: "core", Kind: "Secret", Namespace: "demo", Name: "token"},
	} {
		if _, err := co.Object(t.Context(), ref); !errors.Is(err, ErrNotFound) {
			t.Errorf("Object(%s) err = %v, want ErrNotFound", ref.ID(), err)
		}
	}
	if n := len(dyn.Actions()); n != 0 {
		t.Errorf("API calls made: %d (%v), want none", n, dyn.Actions())
	}
}

func modelPod() *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web-1", Namespace: "demo"},
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{{Name: "init"}},
			Containers:     []corev1.Container{{Name: "app"}, {Name: "sidecar"}},
		},
	}
}

func TestClusterLogsOptions(t *testing.T) {
	ref := model.Ref{Group: "core", Kind: "Pod", Namespace: "demo", Name: "web-1"}
	for _, tc := range []struct{ container, want string }{{"", "app"}, {"sidecar", "sidecar"}, {"init", "init"}} {
		cs := fake.NewClientset()
		cl := ClusterLogs{Model: mapModel{ref.ID(): modelPod()}, Client: cs}
		rc, err := cl.Logs(t.Context(), ref, tc.container, MaxTailLines)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, rc)
		_ = rc.Close()
		opts := logOptions(t, cs.Actions())
		if opts.Container != tc.want || opts.TailLines == nil || *opts.TailLines != MaxTailLines ||
			opts.LimitBytes == nil || *opts.LimitBytes != MaxLogBytes || opts.Follow {
			t.Errorf("container %q: options %+v, want container %s, tail %d, limit %d, no follow", tc.container, opts, tc.want, MaxTailLines, MaxLogBytes)
		}
	}
}

func logOptions(t *testing.T, actions []k8stesting.Action) *corev1.PodLogOptions {
	t.Helper()
	for _, a := range actions {
		if g, ok := a.(k8stesting.GenericAction); ok && a.GetSubresource() == "log" {
			if o, ok := g.GetValue().(*corev1.PodLogOptions); ok {
				return o
			}
		}
	}
	t.Fatalf("no pods/log action in %v", actions)
	return nil
}

func TestClusterLogsNotFound(t *testing.T) {
	ref := model.Ref{Group: "core", Kind: "Pod", Namespace: "demo", Name: "web-1"}
	cs := fake.NewClientset()
	for name, cl := range map[string]ClusterLogs{
		"pod not in model":  {Model: mapModel{}, Client: cs},
		"unknown container": {Model: mapModel{ref.ID(): modelPod()}, Client: cs},
	} {
		container := ""
		if name == "unknown container" {
			container = "other"
		}
		if _, err := cl.Logs(context.Background(), ref, container, 10); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: err = %v, want ErrNotFound", name, err)
		}
	}
	if n := len(cs.Actions()); n != 0 {
		t.Errorf("API calls made: %v, want none", cs.Actions())
	}
}
