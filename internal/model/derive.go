package model

import (
	"fmt"
	"maps"
	"slices"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// APIServerID is the hub of the web: the API server always exists, because the watches talk
// to it. Other control-plane parts are inferred from Leases by the sources (Phase 2).
const APIServerID = "cp/apiserver"

// maxOwnerDepth bounds the ownerReference walk; real chains are at most three deep
// (Pod → Job → CronJob), so a longer one is a cycle.
const maxOwnerDepth = 8

// internalKinds are stored for other consumers (replay chains, inferred control plane) but
// neither drawn nor counted.
var internalKinds = map[string]bool{
	"apps/ControllerRevision":        true,
	"coordination.k8s.io/Lease":      true,
	"discovery.k8s.io/EndpointSlice": true,
	"core/Endpoints":                 true,
}

// controller is a workload object that owns pods directly or through another controller.
type controller struct {
	ref      Ref
	meta     metav1.Object
	template *corev1.PodTemplateSpec
}

// app collects what one bubble stands for while the view is built.
type app struct {
	id        string
	top       Ref
	obj       runtime.Object      // the top owner, nil when it is not stored
	selectees []map[string]string // pod and template label sets that selectors match against
	specs     []*corev1.PodSpec   // pod and template specs that reference ConfigMaps and PVCs
	pods      int
	ready     int
	services  []string // Services merged into this app, which name it
}

// builder derives one view from the stored objects; it lives for one derive call.
type builder struct {
	objs      map[string]runtime.Object
	v         view
	nodes     map[string]bool
	pods      []*corev1.Pod
	ctrls     []controller
	sats      []runtime.Object
	apps      map[string]*app
	ctrlApp   map[string]string // "<ns>/<Kind>/<name>" of every controller → app id
	kindCount map[string]int    // "<group>.<Kind>" → objects neither drawn nor internal
}

func derive(objs map[string]runtime.Object) view {
	b := &builder{
		objs:      objs,
		v:         view{entities: map[string]Entity{}, edges: map[string]Edge{}},
		nodes:     map[string]bool{},
		apps:      map[string]*app{},
		ctrlApp:   map[string]string{},
		kindCount: map[string]int{},
	}
	for _, id := range slices.Sorted(maps.Keys(objs)) {
		b.classify(objs[id])
	}
	b.put(Entity{ID: APIServerID, Kind: "APIServer", Group: "cp", Name: "apiserver", FilterGroup: ControlPlane})
	for _, c := range b.ctrls {
		a := b.appFor(c.ref, metav1.GetControllerOfNoCopy(c.meta))
		b.ctrlApp[c.ref.Namespace+"/"+c.ref.Kind+"/"+c.ref.Name] = a.id
		if c.template != nil {
			a.selectees = append(a.selectees, c.template.Labels)
			a.specs = append(a.specs, &c.template.Spec)
		}
	}
	for _, p := range b.pods {
		b.addPod(p)
	}
	b.satellites()
	b.addApps()
	b.addCounters()
	b.fold()
	return b.v
}

func (b *builder) classify(obj runtime.Object) {
	switch o := obj.(type) {
	case *corev1.Node:
		b.nodes[o.Name] = true
		b.put(Entity{
			ID: ID(CoreGroup, "Node", "", o.Name), Kind: "Node", Group: CoreGroup, Name: o.Name,
			Labels: o.Labels, Status: nodeStatus(o), FilterGroup: Nodes,
		})
		b.edge(EdgeMember, APIServerID, ID(CoreGroup, "Node", "", o.Name))
	case *corev1.Pod:
		b.pods = append(b.pods, o)
	default:
		if !b.addController(obj) {
			b.addOther(obj)
		}
	}
}

// addController records a workload controller and reports whether obj was one.
func (b *builder) addController(obj runtime.Object) bool {
	switch o := obj.(type) {
	case *appsv1.Deployment:
		b.addCtrl("apps", "Deployment", o, &o.Spec.Template)
	case *appsv1.StatefulSet:
		b.addCtrl("apps", "StatefulSet", o, &o.Spec.Template)
	case *appsv1.DaemonSet:
		b.addCtrl("apps", "DaemonSet", o, &o.Spec.Template)
	case *appsv1.ReplicaSet:
		b.addCtrl("apps", "ReplicaSet", o, &o.Spec.Template)
	case *batchv1.Job:
		b.addCtrl("batch", "Job", o, &o.Spec.Template)
	case *batchv1.CronJob:
		b.addCtrl("batch", "CronJob", o, &o.Spec.JobTemplate.Spec.Template)
	default:
		return false
	}
	return true
}

// addOther keeps satellite kinds for the satellite pass, counts every other drawable kind
// and ignores the internal ones.
func (b *builder) addOther(obj runtime.Object) {
	ref, ok := identify(obj)
	switch {
	case !ok || internalKinds[ref.Group+"/"+ref.Kind]:
	case satelliteKinds[ref.Group+"/"+ref.Kind]:
		b.sats = append(b.sats, obj)
	default:
		b.kindCount[ref.Group+"."+ref.Kind]++
	}
}

func (b *builder) addCtrl(group, kind string, m metav1.Object, tmpl *corev1.PodTemplateSpec) {
	b.ctrls = append(b.ctrls, controller{
		ref:      Ref{Group: group, Kind: kind, Namespace: m.GetNamespace(), Name: m.GetName()},
		meta:     m,
		template: tmpl,
	})
}

// appFor walks controller ownerReferences from start to the top owner and returns its app.
// An owner that is not stored (not synced yet, or a kind outside the named groups) ends the
// walk and is the top. A Node owner (static mirror pods) counts as no owner.
func (b *builder) appFor(start Ref, owner *metav1.OwnerReference) *app {
	top := start
	for range maxOwnerDepth {
		if owner == nil || (owner.Kind == "Node" && groupOf(owner.APIVersion) == CoreGroup) {
			break
		}
		top = Ref{Group: groupOf(owner.APIVersion), Kind: owner.Kind, Namespace: start.Namespace, Name: owner.Name}
		obj, ok := b.objs[top.ID()]
		if !ok {
			break
		}
		m, err := meta.Accessor(obj)
		if err != nil {
			break
		}
		owner = metav1.GetControllerOfNoCopy(m)
	}
	id := AppID(top.Namespace, top.Kind, top.Name)
	if a, ok := b.apps[id]; ok {
		return a
	}
	a := &app{id: id, top: top, obj: b.objs[top.ID()]}
	b.apps[id] = a
	return a
}

func (b *builder) addPod(p *corev1.Pod) {
	ref := Ref{Group: CoreGroup, Kind: "Pod", Namespace: p.Namespace, Name: p.Name}
	owner := metav1.GetControllerOfNoCopy(p)
	a := b.appFor(ref, owner)
	a.selectees = append(a.selectees, p.Labels)
	a.specs = append(a.specs, &p.Spec)
	a.pods++
	if podReady(p) {
		a.ready++
	}
	e := Entity{
		ID: ref.ID(), Kind: "Pod", Group: CoreGroup, Name: p.Name, Namespace: p.Namespace,
		Labels: p.Labels, Status: podStatus(p), NodeName: p.Spec.NodeName, AppID: a.id, FilterGroup: Pods,
	}
	if owner != nil {
		e.OwnerID = ID(groupOf(owner.APIVersion), owner.Kind, ownerNamespace(owner, p.Namespace), owner.Name)
	}
	b.put(e)
	if p.Spec.NodeName != "" && b.nodes[p.Spec.NodeName] {
		b.edge(EdgeHosts, ID(CoreGroup, "Node", "", p.Spec.NodeName), a.id)
	}
}

// ownerNamespace is "" for a Node owner (cluster-scoped), the dependent's namespace otherwise.
func ownerNamespace(owner *metav1.OwnerReference, ns string) string {
	if owner.Kind == "Node" && groupOf(owner.APIVersion) == CoreGroup {
		return ""
	}
	return ns
}

func (b *builder) addApps() {
	for _, id := range slices.Sorted(maps.Keys(b.apps)) {
		a := b.apps[id]
		name := a.top.Name
		if len(a.services) > 0 {
			name = slices.Min(a.services)
		}
		group := Workloads
		if isSystemNamespace(a.top.Namespace) {
			group = System
		}
		e := Entity{
			ID: id, Kind: a.top.Kind, Group: a.top.Group, Name: name, Namespace: a.top.Namespace,
			Status: fmt.Sprintf("%d/%d ready", a.ready, desiredPods(a.obj, a.pods)), FilterGroup: group,
		}
		if a.top.Kind != "Pod" {
			e.OwnerID = a.top.ID()
		}
		if a.obj != nil {
			if m, err := meta.Accessor(a.obj); err == nil {
				e.Labels = m.GetLabels()
			}
		}
		b.put(e)
	}
}

func (b *builder) addCounters() {
	for _, key := range slices.Sorted(maps.Keys(b.kindCount)) {
		n := b.kindCount[key]
		b.put(Entity{
			ID: "counter/" + key, Kind: "Counter", Group: "counter", Name: key,
			Status: fmt.Sprintf("%d not drawn", n), FilterGroup: System, Count: n,
		})
	}
}

// put stores an entity with its own copy of the labels (never nil, so the wire shape is
// stable, and never shared with the watch cache's object).
func (b *builder) put(e Entity) {
	if e.Labels == nil {
		e.Labels = map[string]string{}
	} else {
		e.Labels = maps.Clone(e.Labels)
	}
	b.v.entities[e.ID] = e
}

func (b *builder) edge(kind EdgeKind, from, to string) {
	e := newEdge(kind, from, to)
	b.v.edges[e.ID] = e
}
