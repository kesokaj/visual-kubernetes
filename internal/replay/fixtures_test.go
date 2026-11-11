package replay

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	mt "github.com/kesokaj/visual-kubernetes/internal/model/modeltest"
)

var t0 = time.Date(2026, time.November, 10, 10, 0, 0, 0, time.UTC)

func at(ms int64) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }

func ups(objs ...runtime.Object) []model.Change {
	out := make([]model.Change, len(objs))
	for i, o := range objs {
		out[i] = model.Change{Obj: o}
	}
	return out
}

func dels(objs ...runtime.Object) []model.Change {
	out := ups(objs...)
	for i := range out {
		out[i].Deleted = true
	}
	return out
}

// managedBy returns a copy of d last written by manager.
func managedBy(d *appsv1.Deployment, manager string) *appsv1.Deployment {
	c := d.DeepCopy()
	c.ManagedFields = []metav1.ManagedFieldsEntry{
		{Manager: "kubectl-client-side-apply", Operation: metav1.ManagedFieldsOperationUpdate, Time: &metav1.Time{Time: t0.Add(-time.Hour)}},
		{Manager: manager, Operation: metav1.ManagedFieldsOperationUpdate, Time: &metav1.Time{Time: t0}, Subresource: "scale"},
	}
	return c
}

// slice is the EndpointSlice of Service ns/svc listing pods as ready endpoints.
func slice(ns, svc string, pods ...*corev1.Pod) *discoveryv1.EndpointSlice {
	s := &discoveryv1.EndpointSlice{
		ObjectMeta:  metav1.ObjectMeta{Namespace: ns, Name: svc + "-abcde", Labels: mt.Labels("kubernetes.io/service-name", svc)},
		AddressType: discoveryv1.AddressTypeIPv4,
	}
	s.OwnerReferences = []metav1.OwnerReference{*mt.ControllerRef("v1", "Service", svc)}
	for i, p := range pods {
		ready := true
		s.Endpoints = append(s.Endpoints, discoveryv1.Endpoint{
			Addresses:  []string{fmt.Sprintf("10.1.0.%d", i+10)},
			Conditions: discoveryv1.EndpointConditions{Ready: &ready},
			TargetRef:  &corev1.ObjectReference{Kind: "Pod", Namespace: p.Namespace, Name: p.Name},
		})
	}
	return s
}

func running(p *corev1.Pod, node string) *corev1.Pod { return mt.Running(mt.Scheduled(p, node), true) }

func scheduledEvent(p *corev1.Pod, ms int64) runtime.Object {
	return mt.Event(mt.Ref("v1", "Pod", p.Namespace, p.Name), corev1.EventTypeNormal, "Scheduled", "assigned", at(ms))
}

// feed primes a builder with the first batch, observes the rest at their times and returns
// every chain event's data in order.
func feed(t *testing.T, prime []model.Change, batches map[int64][]model.Change) []Chain {
	t.Helper()
	b := New()
	b.Prime(prime...)
	var out []Chain
	for _, ms := range sortedKeys(batches) {
		for _, ev := range b.Observe(at(ms), batches[ms]...) {
			if ev.Type != EventChain {
				t.Fatalf("event type %q, want %q", ev.Type, EventChain)
			}
			out = append(out, ev.Data.(Chain))
		}
	}
	return out
}

func sortedKeys(m map[int64][]model.Change) []int64 { return slices.Sorted(maps.Keys(m)) }

// latest returns the newest copy of each chain id, in first-seen order.
func latest(cs []Chain) []Chain {
	idx := map[string]int{}
	var out []Chain
	for _, c := range cs {
		if i, ok := idx[c.ID]; ok {
			out[i] = c
			continue
		}
		idx[c.ID] = len(out)
		out = append(out, c)
	}
	return out
}

// rank is a step's place in the D17 order.
func rank(s Step) int {
	switch {
	case s.Derived:
		return 0
	case s.Verb == VerbScheduled || s.Verb == VerbFailedScheduling:
		return 4
	case s.Verb == VerbStarted:
		return 5
	case s.Verb == VerbReady:
		return 6
	case strings.HasPrefix(s.Object, "discovery.k8s.io/EndpointSlice/"):
		return 7
	case strings.HasPrefix(s.Object, "core/Pod/"):
		return 3
	}
	return 2 // root or controlled object
}

// d17Order checks a chain against plan D17: the four derived steps first, in their order,
// then observed steps whose stages never go back.
func d17Order(steps []Step) error {
	if len(steps) < len(derivedVerbs)+1 {
		return fmt.Errorf("%d steps, want the four derived steps and at least one observed", len(steps))
	}
	for i, v := range derivedVerbs {
		if !steps[i].Derived || steps[i].Verb != v {
			return fmt.Errorf("step %d is %+v, want derived %q", i, steps[i], v)
		}
	}
	for i := len(derivedVerbs); i < len(steps); i++ {
		if steps[i].Derived {
			return fmt.Errorf("step %d is derived after the observed steps began", i)
		}
		if i > len(derivedVerbs) && rank(steps[i]) < rank(steps[i-1]) {
			return fmt.Errorf("step %d (%s %s) comes after %s %s", i, steps[i].Verb, steps[i].Object, steps[i-1].Verb, steps[i-1].Object)
		}
	}
	return nil
}
