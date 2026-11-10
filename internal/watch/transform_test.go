package watch

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

func bulky(apiVersion, kind string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata": map[string]any{
			"name": "x", "namespace": "shop",
			"labels":        map[string]any{"app": "web"},
			"annotations":   map[string]any{lastApplied: "{...}", "keep": "me"},
			"managedFields": []any{map[string]any{"manager": "kubectl"}},
		},
		"data":       map[string]any{"big": "payload"},
		"binaryData": map[string]any{"blob": "AAAA"},
	}}
}

func TestTrimForCache(t *testing.T) {
	cases := []struct {
		name, apiVersion, kind string
		dataKept               bool
	}{
		{"core ConfigMap loses data", "v1", "ConfigMap", false},
		{"other kind keeps data", "v1", "Pod", true},
		{"ConfigMap kind of another group keeps data", "example.com/v1", "ConfigMap", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := trimForCache(bulky(tc.apiVersion, tc.kind))
			if err != nil {
				t.Fatal(err)
			}
			u := out.(*unstructured.Unstructured)
			if _, found, _ := unstructured.NestedFieldNoCopy(u.Object, "metadata", "managedFields"); found {
				t.Error("managedFields kept")
			}
			if ann := u.GetAnnotations(); ann[lastApplied] != "" || ann["keep"] != "me" {
				t.Errorf("annotations = %v, want only last-applied removed", ann)
			}
			if u.GetLabels()["app"] != "web" || u.GetName() != "x" {
				t.Errorf("identity changed: %v", u.Object["metadata"])
			}
			for _, f := range []string{"data", "binaryData"} {
				if _, found := u.Object[f]; found != tc.dataKept {
					t.Errorf("%s present = %v, want %v", f, found, tc.dataKept)
				}
			}
		})
	}
	tomb := cache.DeletedFinalStateUnknown{Key: "shop/x"}
	if out, err := trimForCache(tomb); err != nil || out != tomb {
		t.Errorf("tombstone = %v, %v; want unchanged", out, err)
	}
}

// TestInformerCacheIsTrimmed pins that Start installs the transform: a ConfigMap with data
// and managedFields reaches the sink without them.
func TestInformerCacheIsTrimmed(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "shop", Name: "settings",
			ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "kubectl"}},
		},
		Data: map[string]string{"big": "payload"},
	}
	h := start(t, append(fixtures(), cm)...)
	c, ok := h.rec.find(func(c model.Change) bool { _, ok := c.Obj.(*corev1.ConfigMap); return ok })
	if !ok {
		t.Fatal("sink never got the ConfigMap")
	}
	got := c.Obj.(*corev1.ConfigMap)
	if got.Name != "settings" || len(got.Data) != 0 || len(got.ManagedFields) != 0 {
		t.Errorf("cached ConfigMap = name %q, data %v, managedFields %v; want trimmed", got.Name, got.Data, got.ManagedFields)
	}
}
