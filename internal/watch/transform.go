package watch

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// lastApplied is kubectl's copy of the whole applied manifest; the model never reads it.
const lastApplied = "kubectl.kubernetes.io/last-applied-configuration"

// trimForCache is the informers' transform: it drops the parts of an object the model never
// reads before the object enters the informer cache, so a large ConfigMap or a busy object's
// field history cannot grow the app's memory (security review 2026-11-10, findings 1–2).
// It removes managedFields and the last-applied annotation from every object, and data and
// binaryData from ConfigMaps (the model draws a ConfigMap by name and references only).
// Anything that is not unstructured, such as a tombstone, passes through unchanged.
func trimForCache(obj any) (any, error) {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return obj, nil
	}
	unstructured.RemoveNestedField(u.Object, "metadata", "managedFields")
	unstructured.RemoveNestedField(u.Object, "metadata", "annotations", lastApplied)
	if u.GetKind() == "ConfigMap" && u.GroupVersionKind().Group == "" {
		unstructured.RemoveNestedField(u.Object, "data")
		unstructured.RemoveNestedField(u.Object, "binaryData")
	}
	return u, nil
}
