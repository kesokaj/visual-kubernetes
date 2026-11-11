package replay

import (
	"cmp"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	eventsv1 "k8s.io/api/events/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

// tracked is what the builder remembers of one object between batches.
type tracked struct {
	owner string
	hash  uint64 // spec hash, or the endpoints hash of an EndpointSlice
	pod   podState
}

// open is a chain still gaining steps.
type open struct {
	chain   *Chain
	started time.Time
	seq     int
}

// pending is a step a batch adds, kept apart until the batch ends so it can be put in order.
type pending struct {
	stage stage
	step  Step
}

// Builder turns observed object changes into chains. The zero value is not usable; call New.
type Builder struct {
	objs    map[string]*tracked
	chains  map[string]*open  // by chain id
	of      map[string]string // object id → the chain it last took a step in
	order   []string          // open chain ids, oldest first
	seq     int
	batch   map[string][]pending
	touched []string // chain ids the current batch added to, in first-touch order
}

// New returns an empty builder.
func New() *Builder {
	return &Builder{
		objs:   map[string]*tracked{},
		chains: map[string]*open{},
		of:     map[string]string{},
		batch:  map[string][]pending{},
	}
}

// Prime records the state the cluster is in before observation starts (the initial list, or
// a recording's snapshot) without starting any chain. Without it, every object Observe sees
// first counts as created.
func (b *Builder) Prime(changes ...model.Change) {
	for _, c := range changes {
		o, ok := identify(c.Obj)
		if !ok || c.Deleted || passOf(o) < 0 { // kinds Observe never handles would only pile up
			continue
		}
		t := &tracked{owner: o.owner}
		switch obj := c.Obj.(type) {
		case *corev1.Pod:
			t.pod = podStateOf(obj)
		case *discoveryv1.EndpointSlice:
			t.hash = fieldHash(obj, "endpoints")
		default:
			t.hash = fieldHash(obj, "spec")
		}
		b.objs[o.id] = t
	}
}

// Observe applies one batch of changes seen at time at and returns a `chain` event for every
// chain the batch added steps to, oldest chain first. Within a batch, roots are handled
// before their children, children before pods, pods before EndpointSlices and Events, so a
// batch holding a whole rollout still attributes every step.
func (b *Builder) Observe(at time.Time, changes ...model.Change) []model.Event {
	b.expire(at)
	type item struct {
		c    model.Change
		o    object
		pass int
	}
	var items []item
	for _, c := range changes {
		if o, ok := identify(c.Obj); ok {
			if p := passOf(o); p >= 0 {
				items = append(items, item{c, o, p})
			}
		}
	}
	slices.SortStableFunc(items, func(x, y item) int { return cmp.Compare(x.pass, y.pass) })
	for _, it := range items {
		b.observe(at, it.o, it.c.Obj, it.c.Deleted)
	}
	return b.flush()
}

// passOf is the order a batch handles an object kind in, or -1 for kinds no chain uses.
func passOf(o object) int {
	switch {
	case o.kind == "core/Event" || o.kind == "events.k8s.io/Event":
		return 4
	case o.kind == "discovery.k8s.io/EndpointSlice":
		return 3
	case o.kind == "core/Pod":
		return 2
	case o.owner != "":
		return 1
	case rootKinds[o.kind]:
		return 0
	}
	return -1
}

func (b *Builder) observe(at time.Time, o object, obj runtime.Object, deleted bool) {
	switch v := obj.(type) {
	case *corev1.Pod:
		b.observePod(at, o, v, deleted)
	case *discoveryv1.EndpointSlice:
		b.observeSlice(at, o, v, deleted)
	case *corev1.Event, *eventsv1.Event:
		if !deleted {
			b.observeEvent(at, obj)
		}
	default:
		b.observeObject(at, o, obj, deleted)
	}
}

// start opens a chain for a root change: the four derived steps at once, then the root's own
// step as the batch's first. The oldest open chain closes past maxChains.
func (b *Builder) start(at time.Time, o object, verb, actor string) {
	id := o.id + "@" + atString(at)
	if _, dup := b.chains[id]; dup {
		return // the same root changed twice in one batch: one chain tells it
	}
	c := &Chain{ID: id}
	for _, v := range derivedVerbs {
		c.Steps = append(c.Steps, Step{At: atString(at), Actor: actor, Verb: v, Object: o.id, Derived: true})
	}
	b.seq++
	b.chains[id] = &open{chain: c, started: at, seq: b.seq}
	b.order = append(b.order, id)
	for len(b.order) > maxChains {
		b.close(b.order[0])
	}
	b.of[o.id] = id
	b.add(id, stageRoot, at, actor, verb, o.id)
}

// add queues a step for chain id and maps the object to that chain.
func (b *Builder) add(id string, s stage, at time.Time, actor, verb, object string) {
	if _, ok := b.batch[id]; !ok {
		b.touched = append(b.touched, id)
	}
	b.batch[id] = append(b.batch[id], pending{stage: s, step: Step{At: atString(at), Actor: actor, Verb: verb, Object: object}})
	b.of[object] = id
}

// flush appends each touched chain's queued steps in stage order, up to MaxSteps, and
// returns one event per chain, oldest first.
func (b *Builder) flush() []model.Event {
	var out []*open
	for _, id := range b.touched {
		oc, ok := b.chains[id]
		if !ok {
			continue // closed during the batch
		}
		steps := b.batch[id]
		slices.SortStableFunc(steps, func(x, y pending) int { return cmp.Compare(x.stage, y.stage) })
		for _, p := range steps {
			if len(oc.chain.Steps) < MaxSteps {
				oc.chain.Steps = append(oc.chain.Steps, p.step)
			}
		}
		out = append(out, oc)
	}
	clear(b.batch)
	b.touched = b.touched[:0]
	slices.SortFunc(out, func(x, y *open) int { return cmp.Compare(x.seq, y.seq) })
	evs := make([]model.Event, len(out))
	for i, oc := range out {
		evs[i] = event(oc.chain)
	}
	return evs
}

// expire closes the chains older than chainTTL.
func (b *Builder) expire(at time.Time) {
	for len(b.order) > 0 && at.Sub(b.chains[b.order[0]].started) > chainTTL {
		b.close(b.order[0])
	}
}

// close forgets a chain and every object mapping to it.
func (b *Builder) close(id string) {
	delete(b.chains, id)
	b.order = slices.DeleteFunc(b.order, func(x string) bool { return x == id })
	for obj, c := range b.of {
		if c == id {
			delete(b.of, obj)
		}
	}
}

// chainFor finds the open chain an object's step belongs to. Pods keep the chain they were
// created in (podFirst); other objects follow their controller, walking up to the root, so a
// ReplicaSet scaled by a new Deployment change joins that change's chain.
func (b *Builder) chainFor(id, owner string, podFirst bool) string {
	own := b.of[id]
	if podFirst && own != "" {
		return own
	}
	for depth, cur := 0, owner; cur != "" && depth < 4; depth++ {
		if c := b.of[cur]; c != "" {
			return c
		}
		t, ok := b.objs[cur]
		if !ok {
			break
		}
		cur = t.owner
	}
	return own
}
