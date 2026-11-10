package model

import (
	"time"

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// EventK8s is the SSE event name of one Kubernetes Event (plan Interfaces § SSE events, D32).
const EventK8s = "event"

// K8sEvent is a Kubernetes Event as the page shows it: ID is the D13 id of the object the
// Event is about (its regarding or involved object), At the newest time the Event reports,
// in RFC 3339 UTC.
type K8sEvent struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
	At      string `json:"at"`
}

// K8sEventData is the `event` SSE message's data.
type K8sEventData struct {
	K8sEvent K8sEvent `json:"k8sEvent"`
}

// ApplyEvents applies the changes and returns the SSE events they produce, in order: the
// model's delta events, then one `event` per added or updated Kubernetes Event. Events are
// streamed, never stored (R2); a deleted Event (its TTL ran out) sends nothing.
func (m *Model) ApplyEvents(changes ...Change) []Event {
	out := DeltaEvents(m.Apply(changes...))
	for _, c := range changes {
		if c.Deleted {
			continue
		}
		if e, ok := KubeEvent(c.Obj); ok {
			out = append(out, Event{Type: EventK8s, Data: K8sEventData{K8sEvent: e}})
		}
	}
	return out
}

// KubeEvent converts an events.k8s.io/v1 or core/v1 Event to its streamed shape. It reports
// false for any other object and for an Event that names no object kind or name.
func KubeEvent(obj runtime.Object) (K8sEvent, bool) {
	switch e := obj.(type) {
	case *eventsv1.Event:
		return kubeEvent(e.Regarding, e.Type, e.Reason, e.Note,
			e.EventTime.Time, seriesTime(e.Series), e.DeprecatedLastTimestamp.Time, e.CreationTimestamp.Time)
	case *corev1.Event:
		var series time.Time
		if e.Series != nil {
			series = e.Series.LastObservedTime.Time
		}
		return kubeEvent(e.InvolvedObject, e.Type, e.Reason, e.Message,
			e.EventTime.Time, series, e.LastTimestamp.Time, e.FirstTimestamp.Time, e.CreationTimestamp.Time)
	}
	return K8sEvent{}, false
}

func seriesTime(s *eventsv1.EventSeries) time.Time {
	if s == nil {
		return time.Time{}
	}
	return s.LastObservedTime.Time
}

func kubeEvent(about corev1.ObjectReference, typ, reason, message string, times ...time.Time) (K8sEvent, bool) {
	// an Event about a Secret can quote its name or content, and Secrets stay out of the
	// stream (PRD R2), so it is dropped whatever its group
	if about.Kind == "" || about.Name == "" || about.Kind == "Secret" {
		return K8sEvent{}, false
	}
	var at time.Time
	for _, t := range times { // the newest, so a repeating Event's series moves it forward
		if t.After(at) {
			at = t
		}
	}
	return K8sEvent{
		ID:      ID(groupOf(about.APIVersion), about.Kind, about.Namespace, about.Name),
		Type:    typ,
		Reason:  reason,
		Message: message,
		At:      at.UTC().Format(time.RFC3339),
	}, true
}
