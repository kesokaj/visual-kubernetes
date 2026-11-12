package hubble

import (
	"cmp"
	"slices"
	"strings"

	"github.com/cilium/cilium/api/v1/flow"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

// ModelReader is the read surface of internal/model the resolver needs.
type ModelReader interface {
	Snapshot() ([]model.Entity, []model.Edge)
	Object(id string) (runtime.Object, bool)
}

type modelIndex struct {
	model    ModelReader
	drawn    map[string]model.Entity
	podApp   map[string]string
	appName  map[string]string   // "<ns>/<name>" -> drawn app or standalone Service id
	services map[string][]string // drawn app id -> Services that front it (via.go)
}

func newIndex(m ModelReader) *modelIndex {
	ents, edges := m.Snapshot()
	idx := &modelIndex{
		model:   m,
		drawn:   make(map[string]model.Entity, len(ents)),
		podApp:  make(map[string]string, len(ents)),
		appName: make(map[string]string, len(ents)),
	}
	for _, e := range ents {
		if strings.HasPrefix(e.ID, "counter/") || e.Group == "counter" {
			continue
		}
		if !strings.HasPrefix(e.ID, "app/") && e.Kind == "Pod" && e.Group == model.CoreGroup {
			if e.AppID != "" {
				idx.podApp[e.ID] = e.AppID
			}
			continue
		}
		idx.drawn[e.ID] = e
		if strings.HasPrefix(e.ID, "app/") && e.Namespace != "" && e.Name != "" {
			key := e.Namespace + "/" + e.Name
			if _, clash := idx.appName[key]; clash {
				idx.appName[key] = ""
			} else {
				idx.appName[key] = e.ID
			}
		}
	}
	idx.services = appServices(idx.drawn, edges)
	for _, e := range ents {
		if e.Kind == "Service" && e.Group == model.CoreGroup && e.Namespace != "" {
			key := e.Namespace + "/" + e.Name
			if _, exists := idx.appName[key]; !exists {
				idx.appName[key] = e.ID
			}
		}
	}
	return idx
}

func (idx *modelIndex) resolve(ep *flow.Endpoint, svc *flow.Service) string {
	if isWorld(ep) {
		return idx.internet()
	}
	if ep != nil && ep.GetNamespace() != "" {
		ns := ep.GetNamespace()
		if pod := ep.GetPodName(); pod != "" {
			if appID, ok := idx.podApp[model.ID(model.CoreGroup, "Pod", ns, pod)]; ok && idx.isDrawn(appID) {
				return appID
			}
		}
		for _, w := range ep.GetWorkloads() {
			if w.GetKind() == "" || w.GetName() == "" {
				continue
			}
			appID := model.AppID(ns, w.GetKind(), w.GetName())
			if idx.isDrawn(appID) {
				return appID
			}
		}
	}
	if svc != nil && svc.GetNamespace() != "" && svc.GetName() != "" {
		return idx.resolveName(svc.GetNamespace(), svc.GetName())
	}
	return ""
}

func (idx *modelIndex) resolveName(ns, name string) string {
	if ns == "" || name == "" {
		return ""
	}
	if id, ok := idx.appName[ns+"/"+name]; ok && idx.isDrawn(id) {
		return id
	}
	for _, kind := range []string{"Deployment", "StatefulSet", "DaemonSet", "CronJob", "Job", "Pod"} {
		if id := model.AppID(ns, kind, name); idx.isDrawn(id) {
			return id
		}
	}
	return idx.resolveServiceSelector(ns, name)
}

func (idx *modelIndex) resolveServiceSelector(ns, name string) string {
	obj, ok := idx.model.Object(model.ID(model.CoreGroup, "Service", ns, name))
	if !ok {
		return ""
	}
	svc, ok := obj.(*corev1.Service)
	if !ok || len(svc.Spec.Selector) == 0 {
		return ""
	}
	sel := labels.SelectorFromSet(svc.Spec.Selector)
	match := ""
	for id, e := range idx.drawn {
		if !strings.HasPrefix(id, "app/") || e.Namespace != ns || len(e.Labels) == 0 {
			continue
		}
		if sel.Matches(labels.Set(e.Labels)) {
			if match != "" {
				return ""
			}
			match = id
		}
	}
	return match
}

func (idx *modelIndex) isDrawn(id string) bool {
	_, ok := idx.drawn[id]
	return ok
}

type edgePair struct{ src, dst string }

type edgeBucket struct {
	dropped int64
	fwdIn   int64
	fwdOut  int64
	vias    map[string]int64 // Service id -> flows addressed through it (D1')
}

// Aggregator folds raw Hubble flows over one window into per-edge FORWARDED and DROPPED counts.
type Aggregator struct {
	buckets map[edgePair]*edgeBucket
}

// NewAggregator returns an empty flow aggregator.
func NewAggregator() *Aggregator {
	return &Aggregator{buckets: map[edgePair]*edgeBucket{}}
}

// Add records one Hubble flow when it is a forward-direction FORWARDED or DROPPED flow between
// two distinct drawn entities.
func (a *Aggregator) Add(f *flow.Flow, idx *modelIndex) {
	if f == nil || isReply(f) {
		return
	}
	v := f.GetVerdict()
	if v != flow.Verdict_FORWARDED && v != flow.Verdict_DROPPED {
		return
	}
	src := idx.resolve(f.GetSource(), f.GetSourceService())
	dst := idx.resolve(f.GetDestination(), f.GetDestinationService())
	if src == "" || dst == "" || src == dst {
		return
	}
	b := a.buckets[edgePair{src, dst}]
	if b == nil {
		b = &edgeBucket{}
		a.buckets[edgePair{src, dst}] = b
	}
	if via := idx.via(f, src, dst); via != "" {
		if b.vias == nil {
			b.vias = map[string]int64{}
		}
		b.vias[via]++
	}
	if v == flow.Verdict_DROPPED {
		b.dropped++
		return
	}
	if f.GetTrafficDirection() == flow.TrafficDirection_EGRESS {
		b.fwdOut++
	} else {
		b.fwdIn++
	}
}

// Flush drains the window and returns sorted Flow events, each carrying its pair's dominant via. When an edge has DROPPED flows in the
// window, sender-only EGRESS FORWARDED traces are suppressed so an ingress NetworkPolicy drop on
// the destination does not also appear as forwarded traffic.
func (a *Aggregator) Flush() []Flow {
	if len(a.buckets) == 0 {
		return nil
	}
	pairs := make([]edgePair, 0, len(a.buckets))
	for p := range a.buckets {
		pairs = append(pairs, p)
	}
	slices.SortFunc(pairs, func(x, y edgePair) int {
		if c := cmp.Compare(x.src, y.src); c != 0 {
			return c
		}
		return cmp.Compare(x.dst, y.dst)
	})
	out := make([]Flow, 0, len(pairs))
	for _, p := range pairs {
		b := a.buckets[p]
		via := dominantVia(b.vias)
		if b.dropped > 0 {
			out = append(out, Flow{Src: p.src, Dst: p.dst, Via: via, Verdict: VerdictDropped, Count: b.dropped})
			if b.fwdIn > 0 {
				out = append(out, Flow{Src: p.src, Dst: p.dst, Via: via, Verdict: VerdictForwarded, Count: b.fwdIn})
			}
			continue
		}
		if fwd := max(b.fwdIn, b.fwdOut); fwd > 0 {
			out = append(out, Flow{Src: p.src, Dst: p.dst, Via: via, Verdict: VerdictForwarded, Count: fwd})
		}
	}
	clear(a.buckets)
	return out
}

func isReply(f *flow.Flow) bool {
	if ir := f.GetIsReply(); ir != nil {
		return ir.GetValue()
	}
	return false
}
