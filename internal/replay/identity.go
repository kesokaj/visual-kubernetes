package replay

import (
	"encoding/json"
	"hash/fnv"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

// rootKinds are the workload kinds that start a chain when nothing controls them.
var rootKinds = map[string]bool{
	"apps/Deployment":  true,
	"apps/StatefulSet": true,
	"apps/DaemonSet":   true,
	"apps/ReplicaSet":  true,
	"batch/Job":        true,
	"batch/CronJob":    true,
	"core/Pod":         true,
}

// object is what the builder needs from one watched object.
type object struct {
	id    string
	kind  string // <group>/<Kind>
	meta  metav1.Object
	owner string // the controller's D13 id, "" when nothing controls it
}

// identify reads an object's D13 id, kind and controller. Typed objects without TypeMeta
// (fixtures, tests) are resolved through client-go's scheme, as the model does.
func identify(obj runtime.Object) (object, bool) {
	acc, err := meta.Accessor(obj)
	if err != nil {
		return object{}, false
	}
	gvk := obj.GetObjectKind().GroupVersionKind()
	if gvk.Kind == "" {
		kinds, _, err := scheme.Scheme.ObjectKinds(obj)
		if err != nil || len(kinds) == 0 {
			return object{}, false
		}
		gvk = kinds[0]
	}
	group := groupName(gvk.Group)
	o := object{
		id:   model.ID(group, gvk.Kind, acc.GetNamespace(), acc.GetName()),
		kind: group + "/" + gvk.Kind,
		meta: acc,
	}
	if ref := metav1.GetControllerOfNoCopy(acc); ref != nil {
		o.owner = model.ID(groupOf(ref.APIVersion), ref.Kind, acc.GetNamespace(), ref.Name)
	}
	return o, true
}

func groupName(g string) string {
	if g == "" {
		return model.CoreGroup
	}
	return g
}

// groupOf maps an ownerReference apiVersion to its D13 group.
func groupOf(apiVersion string) string {
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return model.CoreGroup
	}
	return groupName(gv.Group)
}

// fieldHash hashes one top-level field of obj (spec, endpoints) so a change to it can be told
// from a status-only update without keeping the field itself. A missing field hashes as null.
func fieldHash(obj runtime.Object, field string) uint64 {
	var v any
	if u, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj); err == nil {
		v = u[field]
	}
	b, _ := json.Marshal(v) // map keys are sorted, so equal fields hash equal
	h := fnv.New64a()
	h.Write(b)
	return h.Sum64()
}

// manager returns the newest managedFields manager of o, or fallback when it lists none:
// the client whose write the derived steps stand for.
func manager(o metav1.Object, fallback string) string {
	name, found := fallback, false
	var newest time.Time
	for _, f := range o.GetManagedFields() {
		if f.Manager == "" {
			continue
		}
		var t time.Time
		if f.Time != nil {
			t = f.Time.Time
		}
		if !found || !t.Before(newest) { // a tie goes to the later entry
			name, newest, found = f.Manager, t, true
		}
	}
	return name
}
