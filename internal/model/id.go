package model

import (
	"errors"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
)

// CoreGroup names the core ("") API group in entity ids and the Entity.Group field (D13).
const CoreGroup = "core"

// clusterScoped is the namespace segment of a cluster-scoped object's id (D13).
const clusterScoped = "_"

// Ref identifies one Kubernetes object by group, kind, namespace and name. Group is "core" for
// the core API group and Namespace is "" for cluster-scoped objects.
type Ref struct {
	Group     string
	Kind      string
	Namespace string
	Name      string
}

// ID returns the D13 entity id `<group>/<kind>/<namespace>/<name>`.
func (r Ref) ID() string { return ID(r.Group, r.Kind, r.Namespace, r.Name) }

// ID builds a D13 entity id, mapping the core group "" to "core" and an empty namespace to "_".
func ID(group, kind, namespace, name string) string {
	if group == "" {
		group = CoreGroup
	}
	if namespace == "" {
		namespace = clusterScoped
	}
	return group + "/" + kind + "/" + namespace + "/" + name
}

// AppID returns the synthetic id of the app bubble for a top owner: app/<namespace>/<Kind>/<name>
// (D13 as amended by D23). The kind keeps a bare Pod and a Deployment of the same name apart.
func AppID(namespace, kind, name string) string {
	return appPrefix + "/" + namespace + "/" + kind + "/" + name
}

const appPrefix = "app"

// ErrMalformedID reports an id that is not a D13 object id.
var ErrMalformedID = errors.New("malformed entity id")

// ParseID parses a D13 object id; synthetic ids (app/, cp/, counter/) are not objects and are
// rejected. Kubernetes names never contain "/", so exactly four non-empty segments are required.
func ParseID(id string) (Ref, error) {
	parts := strings.Split(id, "/")
	if len(parts) != 4 {
		return Ref{}, fmt.Errorf("%w: %q has %d segments, want 4", ErrMalformedID, id, len(parts))
	}
	for _, p := range parts {
		if p == "" || strings.ContainsFunc(p, isSpaceOrControl) {
			return Ref{}, fmt.Errorf("%w: %q has an empty or invalid segment", ErrMalformedID, id)
		}
	}
	if parts[0] == appPrefix {
		return Ref{}, fmt.Errorf("%w: %q is an app bubble, not an object", ErrMalformedID, id)
	}
	r := Ref{Group: parts[0], Kind: parts[1], Namespace: parts[2], Name: parts[3]}
	if r.Namespace == clusterScoped {
		r.Namespace = ""
	}
	return r, nil
}

func isSpaceOrControl(r rune) bool { return r <= ' ' || r == 0x7f }

// identify returns an object's Ref. Typed objects decoded without TypeMeta (fixtures, tests)
// are resolved through client-go's scheme; unstructured objects carry their own GVK.
func identify(obj runtime.Object) (Ref, bool) {
	acc, err := meta.Accessor(obj)
	if err != nil {
		return Ref{}, false
	}
	gvk := obj.GetObjectKind().GroupVersionKind()
	if gvk.Kind == "" {
		kinds, _, err := scheme.Scheme.ObjectKinds(obj)
		if err != nil || len(kinds) == 0 {
			return Ref{}, false
		}
		gvk = kinds[0]
	}
	group := gvk.Group
	if group == "" {
		group = CoreGroup
	}
	return Ref{Group: group, Kind: gvk.Kind, Namespace: acc.GetNamespace(), Name: acc.GetName()}, true
}

// groupOf maps an ownerReference apiVersion to its D13 group.
func groupOf(apiVersion string) string {
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil || gv.Group == "" {
		return CoreGroup
	}
	return gv.Group
}
