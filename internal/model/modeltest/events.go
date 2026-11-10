package modeltest

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Ref names the object an Event is about.
func Ref(apiVersion, kind, ns, name string) corev1.ObjectReference {
	return corev1.ObjectReference{APIVersion: apiVersion, Kind: kind, Namespace: ns, Name: name}
}

// Event is an events.k8s.io/v1 Event about ref, emitted at at. Like the API server, it lives
// in its object's namespace, or in "default" when the object is cluster-scoped.
func Event(ref corev1.ObjectReference, typ, reason, note string, at time.Time) *eventsv1.Event {
	ns := ref.Namespace
	if ns == "" {
		ns = metav1.NamespaceDefault
	}
	return &eventsv1.Event{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: fmt.Sprintf("%s.%x", ref.Name, at.UnixNano())},
		EventTime:  metav1.NewMicroTime(at),
		Regarding:  ref,
		Type:       typ,
		Reason:     reason,
		Note:       note,
		Action:     reason,
	}
}
