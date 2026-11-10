package watch

import (
	"context"
	"slices"
	"unicode/utf8"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Kubernetes serves the same Events through two APIs, core/v1 and events.k8s.io/v1; watching
// both caches every Event twice and streams it twice (plan D29). events.k8s.io is preferred.
const eventsGroup = "events.k8s.io"

// skipDuplicateEvents is the reason core/v1 events are not watched when events.k8s.io is.
const skipDuplicateEvents = "duplicate: Events are watched through events.k8s.io"

func isEvents(gvr schema.GroupVersionResource) bool {
	return gvr.Resource == "events" && (gvr.Group == "" || gvr.Group == eventsGroup)
}

// oneEventAPI keeps a single Events resource in gvrs: events.k8s.io when it can be listed,
// else core/v1. The dropped one is recorded as skipped; a resource that is not discovered
// changes nothing.
func (w *Watcher) oneEventAPI(ctx context.Context, gvrs []schema.GroupVersionResource) []schema.GroupVersionResource {
	core := slices.IndexFunc(gvrs, func(g schema.GroupVersionResource) bool { return isEvents(g) && g.Group == "" })
	modern := slices.IndexFunc(gvrs, func(g schema.GroupVersionResource) bool { return isEvents(g) && g.Group == eventsGroup })
	if core < 0 || modern < 0 {
		return gvrs
	}
	drop, reason := gvrs[core], skipDuplicateEvents
	if r := w.check(ctx, gvrs[modern]); r != "" {
		drop, reason = gvrs[modern], r // core/v1 stays and is checked like every other kind
	}
	w.skip(drop, reason)
	return slices.DeleteFunc(slices.Clone(gvrs), func(g schema.GroupVersionResource) bool { return g == drop })
}

// maxEventMessage bounds an Event's message in the cache: events.k8s.io caps a note at 1 kB,
// but core/v1 accepts a message of any length.
const maxEventMessage = 1024

// eventFields are the parts of an Event the stream sends (model.KubeEvent), per API. Every
// other field is dropped before the Event enters the informer cache.
var eventFields = map[string][][]string{
	eventsGroup: {
		{"type"}, {"reason"}, {"note"}, {"regarding", "apiVersion"}, {"regarding", "kind"},
		{"regarding", "namespace"}, {"regarding", "name"}, {"eventTime"}, {"series", "lastObservedTime"},
		{"deprecatedLastTimestamp"},
	},
	"": {
		{"type"}, {"reason"}, {"message"}, {"involvedObject", "apiVersion"}, {"involvedObject", "kind"},
		{"involvedObject", "namespace"}, {"involvedObject", "name"}, {"eventTime"}, {"series", "lastObservedTime"},
		{"firstTimestamp"}, {"lastTimestamp"},
	},
}

// identity is what an informer needs to key, version and type a cached object.
var identity = [][]string{
	{"apiVersion"}, {"kind"}, {"metadata", "name"}, {"metadata", "namespace"}, {"metadata", "uid"},
	{"metadata", "resourceVersion"}, {"metadata", "creationTimestamp"},
}

// trimEvent replaces an Event's content with its identity and streamed fields, the message
// capped at maxEventMessage bytes. It reports false for any object that is not an Event.
func trimEvent(u *unstructured.Unstructured) bool {
	fields, ok := eventFields[u.GroupVersionKind().Group]
	if !ok || u.GetKind() != "Event" {
		return false
	}
	out := map[string]any{}
	for _, path := range slices.Concat(identity, fields) {
		if v, found, _ := unstructured.NestedFieldNoCopy(u.Object, path...); found && v != nil {
			if s, ok := v.(string); ok && (path[0] == "note" || path[0] == "message") {
				v = truncate(s, maxEventMessage)
			}
			_ = unstructured.SetNestedField(out, v, path...) // fails only on a non-map parent, which out never has
		}
	}
	u.Object = out
	return true
}

// truncate cuts s to at most n bytes without splitting a UTF-8 sequence.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
