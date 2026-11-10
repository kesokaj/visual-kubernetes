package watch

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	mt "github.com/kesokaj/visual-kubernetes/internal/model/modeltest"
)

var (
	coreEventsGVR = schema.GroupVersionResource{Version: "v1", Resource: "events"}
	eventsGVR     = schema.GroupVersionResource{Group: eventsGroup, Version: "v1", Resource: "events"}
)

// eventsDiscovery serves pods plus Events through both APIs, as every cluster does.
func eventsDiscovery() *fakediscovery.FakeDiscovery {
	return &fakediscovery.FakeDiscovery{Fake: &k8stesting.Fake{Resources: []*metav1.APIResourceList{
		{GroupVersion: "v1", APIResources: []metav1.APIResource{lw("pods", "Pod", true), lw("events", "Event", true)}},
		{GroupVersion: "events.k8s.io/v1", APIResources: []metav1.APIResource{lw("events", "Event", true)}},
	}}}
}

// bulkyEvent is an events.k8s.io Event carrying fields the stream never sends.
func bulkyEvent() *eventsv1.Event {
	ev := mt.Event(mt.Ref("apps/v1", "Deployment", "shop", "web"), "Normal", "ScalingReplicaSet", strings.Repeat("é", 600), time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC))
	ev.ManagedFields = []metav1.ManagedFieldsEntry{{Manager: "kube-controller-manager"}}
	ev.Labels = map[string]string{"big": "label"}
	ev.ReportingController = "deployment-controller"
	ev.Related = &corev1.ObjectReference{Kind: "ReplicaSet", Name: "web-7d4b9c"}
	return ev
}

func startEvents(t *testing.T, dyn *dynamicfake.FakeDynamicClient) (*Watcher, *recorder) {
	t.Helper()
	rec := &recorder{}
	w := New(eventsDiscovery(), dyn, rec.sink, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); w.Wait() })
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "initial sync", w.Synced)
	return w, rec
}

// TestEventsWatchedThroughOneAPI (plan D29): with both Event APIs served, only events.k8s.io
// is watched, core/v1 is skipped as a duplicate and never listed past the check, and the
// Event reaches the sink typed and trimmed to the streamed fields.
func TestEventsWatchedThroughOneAPI(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClient(scheme.Scheme, bulkyEvent())
	w, rec := startEvents(t, dyn)
	if got, want := w.Watched(), []schema.GroupVersionResource{podsGVR, eventsGVR}; !slices.Equal(got, want) {
		t.Errorf("Watched() = %v, want %v", got, want)
	}
	if got := w.Skipped(); len(got) != 1 || got[0].Resource != coreEventsGVR || got[0].Reason != skipDuplicateEvents {
		t.Errorf("Skipped() = %v, want only core events as a duplicate", got)
	}
	for _, a := range dyn.Actions() {
		if a.GetResource() == coreEventsGVR {
			t.Errorf("core/v1 events were read: %s", a.GetVerb())
		}
	}
	c, ok := rec.find(func(c model.Change) bool { _, ok := c.Obj.(*eventsv1.Event); return ok })
	if !ok {
		t.Fatal("sink never got the Event")
	}
	got := c.Obj.(*eventsv1.Event)
	if len(got.ManagedFields) != 0 || len(got.Labels) != 0 || got.ReportingController != "" || got.Related != nil || got.Action != "" {
		t.Errorf("cached Event kept fields the stream never sends: %+v", got)
	}
	if got.Regarding.Name != "web" || got.Reason != "ScalingReplicaSet" || got.EventTime.IsZero() || len(got.Note) != maxEventMessage {
		t.Errorf("cached Event lost streamed fields: regarding %+v reason %q time %v note %d bytes", got.Regarding, got.Reason, got.EventTime, len(got.Note))
	}
}

// TestEventsFallBackToCore: when events.k8s.io cannot be listed, core/v1 Events are watched.
func TestEventsFallBackToCore(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClient(scheme.Scheme)
	dyn.PrependReactor("list", "events", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if a.GetResource().Group != eventsGroup {
			return false, nil, nil
		}
		return true, nil, apierrors.NewForbidden(eventsGVR.GroupResource(), "", errors.New("RBAC"))
	})
	w, _ := startEvents(t, dyn)
	if got, want := w.Watched(), []schema.GroupVersionResource{coreEventsGVR, podsGVR}; !slices.Equal(got, want) {
		t.Errorf("Watched() = %v, want %v", got, want)
	}
	if got := w.Skipped(); len(got) != 1 || got[0].Resource != eventsGVR || got[0].Reason != "forbidden" {
		t.Errorf("Skipped() = %v, want only events.k8s.io forbidden", got)
	}
}

func TestTrimEvent(t *testing.T) {
	core := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Event",
		"metadata":       map[string]any{"name": "web.1", "namespace": "shop", "resourceVersion": "7", "labels": map[string]any{"a": "b"}},
		"involvedObject": map[string]any{"apiVersion": "v1", "kind": "Pod", "name": "web-1", "namespace": "shop", "uid": "u", "fieldPath": "spec"},
		"type":           "Warning", "reason": "BackOff", "message": "ok", "count": int64(4), "lastTimestamp": "2026-11-10T10:00:00Z",
		"source": map[string]any{"component": "kubelet"},
	}}
	if !trimEvent(core) {
		t.Fatal("core/v1 Event not trimmed")
	}
	want := map[string]any{
		"apiVersion": "v1", "kind": "Event",
		"metadata":       map[string]any{"name": "web.1", "namespace": "shop", "resourceVersion": "7"},
		"involvedObject": map[string]any{"apiVersion": "v1", "kind": "Pod", "name": "web-1", "namespace": "shop"},
		"type":           "Warning", "reason": "BackOff", "message": "ok", "lastTimestamp": "2026-11-10T10:00:00Z",
	}
	if !reflect.DeepEqual(core.Object, want) {
		t.Errorf("trimmed core Event = %v, want %v", core.Object, want)
	}
	for _, u := range []*unstructured.Unstructured{bulky("v1", "Pod"), bulky("example.com/v1", "Event")} {
		if trimEvent(u) {
			t.Errorf("%s %s trimmed as an Event", u.GetAPIVersion(), u.GetKind())
		}
	}
}

func TestTruncateKeepsRunes(t *testing.T) {
	for _, tc := range []struct {
		in   string
		n    int
		want string
	}{{"abc", 5, "abc"}, {"abcdef", 3, "abc"}, {"aé", 2, "a"}, {"éé", 3, "é"}} {
		if got := truncate(tc.in, tc.n); got != tc.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}

// TestSameVersion: a relist hands every cached object back as an update with the same
// resourceVersion; only those are dropped. An empty version (fake clients) is never equal.
func TestSameVersion(t *testing.T) {
	rv := func(v string) *unstructured.Unstructured {
		u := &unstructured.Unstructured{Object: map[string]any{}}
		u.SetResourceVersion(v)
		return u
	}
	for _, tc := range []struct {
		name     string
		old, obj any
		want     bool
	}{
		{"same version", rv("5"), rv("5"), true},
		{"new version", rv("5"), rv("6"), false},
		{"no version", rv(""), rv(""), false},
		{"not unstructured", "x", rv("5"), false},
	} {
		if got := sameVersion(tc.old, tc.obj); got != tc.want {
			t.Errorf("%s: sameVersion = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestHandlerDropsRelistUpdates: the informer handler queues a real update but not a relist's
// unchanged object (review 2026-11-10-1509, finding 1).
func TestHandlerDropsRelistUpdates(t *testing.T) {
	cm := func(rv string) *unstructured.Unstructured {
		u := &unstructured.Unstructured{}
		u.SetAPIVersion("v1")
		u.SetKind("ConfigMap")
		u.SetNamespace("shop")
		u.SetName("c")
		u.SetResourceVersion(rv)
		return u
	}
	w := New(nil, nil, nil, nil)
	h := w.handler()
	h.OnUpdate(cm("5"), cm("5"))
	if n := len(w.q.take()); n != 0 {
		t.Errorf("relist update queued %d changes, want 0", n)
	}
	h.OnUpdate(cm("5"), cm("6"))
	if n := len(w.q.take()); n != 1 {
		t.Errorf("real update queued %d changes, want 1", n)
	}
}
