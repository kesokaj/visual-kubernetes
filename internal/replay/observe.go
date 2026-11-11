package replay

import (
	"cmp"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

// podState is what a pod's steps are derived from.
type podState struct {
	scheduled bool
	started   bool
	ready     bool
	failed    bool // a FailedScheduling Event was seen
	node      string
}

func podStateOf(p *corev1.Pod) podState {
	s := podState{node: p.Spec.NodeName, scheduled: p.Spec.NodeName != ""}
	s.started = p.Status.Phase == corev1.PodRunning
	for _, cs := range p.Status.ContainerStatuses {
		s.started = s.started || cs.State.Running != nil
	}
	for _, c := range p.Status.Conditions {
		switch c.Type {
		case corev1.PodScheduled:
			s.scheduled = s.scheduled || c.Status == corev1.ConditionTrue
		case corev1.PodReady:
			s.ready = c.Status == corev1.ConditionTrue
		}
	}
	return s
}

// observeObject handles every non-pod kind a chain uses: a root's create, spec change or
// delete starts a chain; a controlled object's joins its controller's chain.
func (b *Builder) observeObject(at time.Time, o object, obj runtime.Object, deleted bool) {
	prev, seen := b.objs[o.id]
	if deleted {
		delete(b.objs, o.id)
		if !seen {
			return
		}
		if o.owner == "" && rootKinds[o.kind] {
			b.start(at, o, VerbDeleted, actorClient) // the deleter is never recorded on the object
		} else if id := b.chainFor(o.id, prev.owner, false); id != "" {
			b.add(id, stageChild, at, actorController, VerbDeleted, o.id)
		}
		return
	}
	hash := fieldHash(obj, "spec")
	b.objs[o.id] = &tracked{owner: o.owner, hash: hash}
	verb := VerbCreated
	if seen {
		if prev.hash == hash {
			return // status or metadata only: not a step
		}
		verb = VerbUpdated
	}
	if o.owner == "" {
		b.start(at, o, verb, manager(o.meta, actorClient))
	} else if id := b.chainFor(o.id, o.owner, false); id != "" {
		b.add(id, stageChild, at, manager(o.meta, actorController), verb, o.id)
	}
}

// observePod adds a pod's created, scheduled, started and ready steps to its chain, each at
// the batch the pod first shows it. A bare pod is a root and starts its own chain.
func (b *Builder) observePod(at time.Time, o object, p *corev1.Pod, deleted bool) {
	prev, seen := b.objs[o.id]
	if deleted {
		delete(b.objs, o.id)
		if !seen {
			return
		}
		// Owner first: a scale-down is the owner's newest chain, not the one the pod was created in.
		if o.owner == "" {
			b.start(at, o, VerbDeleted, actorClient)
		} else if id := b.chainFor(o.id, prev.owner, false); id != "" {
			b.add(id, stagePod, at, actorController, VerbDeleted, o.id)
		}
		return
	}
	var was podState
	if seen {
		was = prev.pod
	}
	now := podStateOf(p)
	now.scheduled = now.scheduled || was.scheduled // a bound pod stays bound; an Event may say so first
	now.failed = was.failed
	b.objs[o.id] = &tracked{owner: o.owner, pod: now}
	if o.owner == "" && !seen {
		b.start(at, o, VerbCreated, manager(o.meta, actorClient))
	}
	id := b.chainFor(o.id, o.owner, true)
	if id == "" {
		return
	}
	if !seen && o.owner != "" {
		b.add(id, stagePod, at, manager(o.meta, actorController), VerbCreated, o.id)
	}
	if now.scheduled && !was.scheduled {
		b.add(id, stageScheduled, at, actorScheduler, VerbScheduled, o.id)
	}
	if now.started && !was.started {
		b.add(id, stageStarted, at, kubelet(now.node), VerbStarted, o.id)
	}
	if now.ready && !was.ready {
		b.add(id, stageReady, at, kubelet(now.node), VerbReady, o.id)
	}
}

func kubelet(node string) string {
	if node == "" {
		return actorKubelet
	}
	return actorKubelet + " on " + node
}

// observeSlice adds an EndpointSlice's change to the newest chain one of its endpoints'
// pods belongs to: the step where a Service starts (or stops) sending traffic to them.
func (b *Builder) observeSlice(at time.Time, o object, s *discoveryv1.EndpointSlice, deleted bool) {
	if deleted {
		delete(b.objs, o.id)
		return
	}
	hash := fieldHash(s, "endpoints")
	prev, seen := b.objs[o.id]
	b.objs[o.id] = &tracked{hash: hash}
	if seen && prev.hash == hash {
		return
	}
	var best *open
	for _, ep := range s.Endpoints {
		ref := ep.TargetRef
		if ref == nil || ref.Kind != "Pod" {
			continue
		}
		oc := b.chains[b.of[model.ID(model.CoreGroup, "Pod", cmp.Or(ref.Namespace, s.Namespace), ref.Name)]]
		if oc != nil && (best == nil || oc.seq > best.seq) {
			best = oc
		}
	}
	if best == nil {
		return
	}
	verb := VerbUpdated
	if !seen {
		verb = VerbCreated
	}
	b.add(best.chain.ID, stageSlice, at, manager(s, actorSlices), verb, o.id)
}

// observeEvent turns the scheduler's Scheduled and FailedScheduling Events into the pod's
// scheduling step, once per pod, whichever of the Event and the binding is seen first.
func (b *Builder) observeEvent(at time.Time, obj runtime.Object) {
	e, ok := model.KubeEvent(obj)
	if !ok || !strings.HasPrefix(e.ID, model.CoreGroup+"/Pod/") {
		return
	}
	t, ok := b.objs[e.ID]
	if !ok {
		return
	}
	id := b.chainFor(e.ID, t.owner, true)
	if id == "" {
		return
	}
	switch e.Reason {
	case "Scheduled":
		if !t.pod.scheduled {
			t.pod.scheduled = true
			b.add(id, stageScheduled, at, actorScheduler, VerbScheduled, e.ID)
		}
	case "FailedScheduling":
		if !t.pod.failed && !t.pod.scheduled {
			t.pod.failed = true
			b.add(id, stageScheduled, at, actorScheduler, VerbFailedScheduling, e.ID)
		}
	}
}
