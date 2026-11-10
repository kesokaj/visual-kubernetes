package model

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	autoscalingv1 "k8s.io/api/autoscaling/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
)

// satelliteKinds attach to the app they serve (D15): an exact single target makes them part of
// the bubble (AppID), several targets give them their own entity with an edge to each.
var satelliteKinds = map[string]bool{
	"core/Service":                             true,
	"core/ConfigMap":                           true,
	"core/PersistentVolumeClaim":               true,
	"autoscaling/HorizontalPodAutoscaler":      true,
	"autoscaling.k8s.io/VerticalPodAutoscaler": true,
	"networking.k8s.io/NetworkPolicy":          true,
	"networking.k8s.io/Ingress":                true,
	"gateway.networking.k8s.io/HTTPRoute":      true,
	"gateway.networking.k8s.io/Gateway":        true,
}

func (b *builder) satellites() {
	appIDs := slices.Sorted(maps.Keys(b.apps))
	// Services first: Ingresses and HTTPRoutes route to the app a Service merged into.
	svcTarget := map[string]string{} // "<ns>/<service>" → app id, or the Service's own id
	for _, obj := range b.sats {
		if s, ok := obj.(*corev1.Service); ok {
			b.addService(s, appIDs, svcTarget)
		}
	}
	cmRefs, pvcRefs := b.specRefIndex()
	for _, obj := range b.sats {
		if !b.addStorageSatellite(obj, cmRefs, pvcRefs) {
			b.addPolicySatellite(obj, appIDs, svcTarget)
		}
	}
}

// addStorageSatellite attaches a ConfigMap or PVC to the apps whose pod specs reference it
// and reports whether obj was one; an unreferenced ConfigMap is only counted.
func (b *builder) addStorageSatellite(obj runtime.Object, cmRefs, pvcRefs map[string][]string) bool {
	switch o := obj.(type) {
	case *corev1.ConfigMap:
		targets := cmRefs[o.Namespace+"/"+o.Name]
		if len(targets) == 0 {
			b.kindCount["core.ConfigMap"]++ // unreferenced: counted, not drawn
			return true
		}
		b.attach(base(o, CoreGroup, "ConfigMap", ServicesConfig, ""), targets, EdgeReferences)
	case *corev1.PersistentVolumeClaim:
		e := base(o, CoreGroup, "PersistentVolumeClaim", ServicesConfig, string(o.Status.Phase))
		b.attach(e, pvcRefs[o.Namespace+"/"+o.Name], EdgeReferences)
	default:
		return false
	}
	return true
}

// addPolicySatellite attaches scalers, NetworkPolicies, Ingresses and the unstructured kinds
// (VPA, Gateway API) to the apps they target, select or route to.
func (b *builder) addPolicySatellite(obj runtime.Object, appIDs []string, svcTarget map[string]string) {
	switch o := obj.(type) {
	case *autoscalingv2.HorizontalPodAutoscaler:
		e := base(o, "autoscaling", "HorizontalPodAutoscaler", ServicesConfig, hpaStatus(o.Status.CurrentReplicas, o.Spec.MinReplicas, o.Spec.MaxReplicas))
		b.addScaler(e, o.Spec.ScaleTargetRef.Kind, o.Spec.ScaleTargetRef.Name)
	case *autoscalingv1.HorizontalPodAutoscaler:
		e := base(o, "autoscaling", "HorizontalPodAutoscaler", ServicesConfig, hpaStatus(o.Status.CurrentReplicas, o.Spec.MinReplicas, o.Spec.MaxReplicas))
		b.addScaler(e, o.Spec.ScaleTargetRef.Kind, o.Spec.ScaleTargetRef.Name)
	case *networkingv1.NetworkPolicy:
		e := base(o, "networking.k8s.io", "NetworkPolicy", ServicesConfig, "")
		sel, err := metav1.LabelSelectorAsSelector(&o.Spec.PodSelector)
		if err != nil {
			b.put(e) // an invalid selector selects nothing
			return
		}
		b.attach(e, b.appsMatching(o.Namespace, sel, appIDs), EdgeSelects)
	case *networkingv1.Ingress:
		e := base(o, "networking.k8s.io", "Ingress", Ingress, "")
		b.attach(e, routeTargets(ingressBackends(o), svcTarget), EdgeRoutes)
	case *unstructured.Unstructured:
		b.addUnstructured(o, svcTarget)
	}
}

func (b *builder) addService(s *corev1.Service, appIDs []string, svcTarget map[string]string) {
	typ := string(s.Spec.Type)
	if typ == "" {
		typ = string(corev1.ServiceTypeClusterIP)
	}
	e := base(s, CoreGroup, "Service", ServicesConfig, typ)
	var targets []string
	if len(s.Spec.Selector) > 0 { // an empty selector means "no selector", never "every pod"
		targets = b.appsMatching(s.Namespace, labels.SelectorFromSet(s.Spec.Selector), appIDs)
	}
	key := s.Namespace + "/" + s.Name
	svcTarget[key] = e.ID
	if len(targets) == 1 {
		svcTarget[key] = targets[0]
		b.apps[targets[0]].services = append(b.apps[targets[0]].services, s.Name)
	}
	b.attach(e, targets, EdgeSelects)
}

// addScaler makes an HPA or VPA part of the app owning its target; a target that is not a
// stored workload leaves it standalone.
func (b *builder) addScaler(e Entity, kind, name string) {
	e.AppID = b.ctrlApp[e.Namespace+"/"+kind+"/"+name]
	b.put(e)
}

func (b *builder) addUnstructured(u *unstructured.Unstructured, svcTarget map[string]string) {
	gvk := u.GroupVersionKind()
	switch gvk.Group + "/" + gvk.Kind {
	case "autoscaling.k8s.io/VerticalPodAutoscaler":
		mode, _, _ := unstructured.NestedString(u.Object, "spec", "updatePolicy", "updateMode")
		kind, _, _ := unstructured.NestedString(u.Object, "spec", "targetRef", "kind")
		name, _, _ := unstructured.NestedString(u.Object, "spec", "targetRef", "name")
		b.addScaler(base(u, gvk.Group, gvk.Kind, ServicesConfig, mode), kind, name)
	case "gateway.networking.k8s.io/Gateway":
		class, _, _ := unstructured.NestedString(u.Object, "spec", "gatewayClassName")
		b.put(base(u, gvk.Group, gvk.Kind, Ingress, class))
	case "gateway.networking.k8s.io/HTTPRoute":
		e := base(u, gvk.Group, gvk.Kind, Ingress, "")
		backends, parents := httpRouteRefs(u)
		b.attach(e, routeTargets(backends, svcTarget), EdgeRoutes)
		for _, gw := range parents {
			ns, name, _ := strings.Cut(gw, "/")
			if id := ID(gvk.Group, "Gateway", ns, name); b.objs[id] != nil {
				b.edge(EdgeParent, id, e.ID)
			}
		}
	}
}

// httpRouteRefs returns "<ns>/<name>" of an HTTPRoute's backend Services and parent Gateways.
func httpRouteRefs(u *unstructured.Unstructured) (backends, parents []string) {
	rules, _, _ := unstructured.NestedSlice(u.Object, "spec", "rules")
	for _, r := range rules {
		rule, _ := r.(map[string]any)
		refs, _ := rule["backendRefs"].([]any)
		for _, ref := range refs {
			if key, ok := objRef(ref, u.GetNamespace(), "Service"); ok {
				backends = append(backends, key)
			}
		}
	}
	refs, _, _ := unstructured.NestedSlice(u.Object, "spec", "parentRefs")
	for _, ref := range refs {
		if key, ok := objRef(ref, u.GetNamespace(), "Gateway"); ok {
			parents = append(parents, key)
		}
	}
	return backends, parents
}

// objRef reads a Gateway API object reference whose kind defaults to want and whose
// namespace defaults to ns; it reports false for another kind or a missing name.
func objRef(it any, ns, want string) (string, bool) {
	ref, ok := it.(map[string]any)
	if !ok {
		return "", false
	}
	kind, _ := ref["kind"].(string)
	name, _ := ref["name"].(string)
	refNS, _ := ref["namespace"].(string)
	if kind == "" {
		kind = want
	}
	if refNS == "" {
		refNS = ns
	}
	return refNS + "/" + name, kind == want && name != ""
}

func ingressBackends(ing *networkingv1.Ingress) []string {
	var out []string
	if d := ing.Spec.DefaultBackend; d != nil && d.Service != nil {
		out = append(out, ing.Namespace+"/"+d.Service.Name)
	}
	for _, r := range ing.Spec.Rules {
		if r.HTTP == nil {
			continue
		}
		for _, p := range r.HTTP.Paths {
			if p.Backend.Service != nil {
				out = append(out, ing.Namespace+"/"+p.Backend.Service.Name)
			}
		}
	}
	return out
}

// routeTargets maps backend Services to the app each merged into, or to the Service itself;
// a backend Service that is not stored is dropped.
func routeTargets(services []string, svcTarget map[string]string) []string {
	var out []string
	for _, s := range services {
		if t, ok := svcTarget[s]; ok {
			out = append(out, t)
		}
	}
	return out
}

// specRefIndex maps "<ns>/<name>" of each referenced ConfigMap and PVC to the apps using it.
func (b *builder) specRefIndex() (cms, pvcs map[string][]string) {
	cmSet, pvcSet := map[string]map[string]bool{}, map[string]map[string]bool{}
	add := func(set map[string]map[string]bool, key, appID string) {
		if set[key] == nil {
			set[key] = map[string]bool{}
		}
		set[key][appID] = true
	}
	for id, a := range b.apps {
		for _, spec := range a.specs {
			c, p := specRefs(spec)
			for _, n := range c {
				add(cmSet, a.top.Namespace+"/"+n, id)
			}
			for _, n := range p {
				add(pvcSet, a.top.Namespace+"/"+n, id)
			}
		}
	}
	cms, pvcs = map[string][]string{}, map[string][]string{}
	for k, s := range cmSet {
		cms[k] = slices.Sorted(maps.Keys(s))
	}
	for k, s := range pvcSet {
		pvcs[k] = slices.Sorted(maps.Keys(s))
	}
	return cms, pvcs
}

// appsMatching returns the apps in ns whose pods or pod templates the selector matches.
func (b *builder) appsMatching(ns string, sel labels.Selector, appIDs []string) []string {
	var out []string
	for _, id := range appIDs {
		a := b.apps[id]
		if a.top.Namespace != ns {
			continue
		}
		for _, l := range a.selectees {
			if sel.Matches(labels.Set(l)) {
				out = append(out, id)
				break
			}
		}
	}
	return out
}

// attach applies D15: one app target → part of that app; otherwise an edge to each target.
func (b *builder) attach(e Entity, targets []string, kind EdgeKind) {
	slices.Sort(targets)
	targets = slices.Compact(targets)
	if len(targets) == 1 && strings.HasPrefix(targets[0], "app/") {
		e.AppID = targets[0]
	} else {
		for _, t := range targets {
			b.edge(kind, e.ID, t)
		}
	}
	b.put(e)
}

func base(m metav1.Object, group, kind string, fg FilterGroup, status string) Entity {
	return Entity{
		ID: ID(group, kind, m.GetNamespace(), m.GetName()), Kind: kind, Group: group,
		Name: m.GetName(), Namespace: m.GetNamespace(), Labels: m.GetLabels(), Status: status, FilterGroup: fg,
	}
}

func hpaStatus(current int32, minReplicas *int32, maxReplicas int32) string {
	lo := int32(1)
	if minReplicas != nil {
		lo = *minReplicas
	}
	return fmt.Sprintf("%d replicas (min %d, max %d)", current, lo, maxReplicas)
}
