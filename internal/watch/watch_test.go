package watch

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8swatch "k8s.io/apimachinery/pkg/watch"
	fakediscovery "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	"github.com/kesokaj/visual-kubernetes/internal/model/modeltest"
)

var (
	podsGVR   = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	scGVR     = schema.GroupVersionResource{Group: "storage.k8s.io", Version: "v1", Resource: "storageclasses"}
	deployGVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
)

func lw(name, kind string, namespaced bool) metav1.APIResource {
	return metav1.APIResource{Name: name, Kind: kind, Namespaced: namespaced, Verbs: metav1.Verbs{"get", "list", "watch"}}
}

// fakeDiscovery serves core, apps, storage and one group the ClusterRole does not name.
func fakeDiscovery() *fakediscovery.FakeDiscovery {
	return &fakediscovery.FakeDiscovery{Fake: &k8stesting.Fake{Resources: []*metav1.APIResourceList{
		{GroupVersion: "v1", APIResources: []metav1.APIResource{
			lw("pods", "Pod", true),
			{Name: "pods/log", Kind: "Pod", Namespaced: true, Verbs: metav1.Verbs{"get"}},
			lw("nodes", "Node", false),
			lw("configmaps", "ConfigMap", true),
			lw("secrets", "Secret", true),
			{Name: "bindings", Kind: "Binding", Namespaced: true, Verbs: metav1.Verbs{"create"}},
		}},
		{GroupVersion: "apps/v1", APIResources: []metav1.APIResource{lw("deployments", "Deployment", true)}},
		{GroupVersion: "storage.k8s.io/v1", APIResources: []metav1.APIResource{lw("storageclasses", "StorageClass", false)}},
		{GroupVersion: "example.com/v1", APIResources: []metav1.APIResource{lw("widgets", "Widget", true)}},
	}}}
}

// recorder is a Sink that keeps every change it is handed.
type recorder struct {
	mu      sync.Mutex
	changes []model.Change
}

func (r *recorder) sink(cs []model.Change) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.changes = append(r.changes, cs...)
}

func (r *recorder) find(pred func(model.Change) bool) (model.Change, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.changes {
		if pred(c) {
			return c, true
		}
	}
	return model.Change{}, false
}

// lockedBuffer is a log destination safe for the informer goroutines.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

type harness struct {
	w    *Watcher
	dyn  *dynamicfake.FakeDynamicClient
	rec  *recorder
	logs *lockedBuffer
	// podWatch is closed once the pod informer's watch is registered with the tracker, so a
	// test that creates a pod afterwards cannot race the list-then-watch gap.
	podWatch chan struct{}
}

func start(t *testing.T, objs ...runtime.Object) *harness {
	t.Helper()
	h := &harness{rec: &recorder{}, logs: &lockedBuffer{}, podWatch: make(chan struct{})}
	h.dyn = dynamicfake.NewSimpleDynamicClient(scheme.Scheme, objs...)
	h.dyn.PrependReactor("list", "storageclasses", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(scGVR.GroupResource(), "", errors.New("RBAC"))
	})
	var once sync.Once
	h.dyn.PrependWatchReactor("pods", func(a k8stesting.Action) (bool, k8swatch.Interface, error) {
		w, err := h.dyn.Tracker().Watch(a.GetResource(), a.GetNamespace())
		once.Do(func() { close(h.podWatch) })
		return true, w, err
	})
	log := slog.New(slog.NewTextHandler(h.logs, nil))
	h.w = New(fakeDiscovery(), h.dyn, h.rec.sink, log)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); h.w.Wait() })
	if err := h.w.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, "initial sync", h.w.Synced)
	return h
}

func fixtures() []runtime.Object {
	sc := &storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: "standard"}}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "db-password"}}
	return []runtime.Object{
		modeltest.Node("node-1", true),
		modeltest.Pod("shop", "web-1", modeltest.Labels("app", "web"), nil),
		modeltest.Deployment("shop", "web", 1, modeltest.Labels("app", "web")),
		secret, sc,
	}
}

func TestWatcherSyncsAroundForbiddenGroup(t *testing.T) {
	h := start(t, fixtures()...)

	for name, pred := range map[string]func(model.Change) bool{
		"typed Node":       func(c model.Change) bool { _, ok := c.Obj.(*corev1.Node); return ok },
		"typed Pod":        func(c model.Change) bool { _, ok := c.Obj.(*corev1.Pod); return ok },
		"typed Deployment": func(c model.Change) bool { _, ok := c.Obj.(*appsv1.Deployment); return ok },
	} {
		if _, ok := h.rec.find(pred); !ok {
			t.Errorf("sink never got a %s", name)
		}
	}
	if c, ok := h.rec.find(func(c model.Change) bool { _, ok := c.Obj.(*storagev1.StorageClass); return ok }); ok {
		t.Errorf("forbidden StorageClass reached the sink: %v", c.Obj)
	}
	skipped := h.w.Skipped()
	if len(skipped) != 1 || skipped[0].Resource != scGVR || skipped[0].Reason != "forbidden" {
		t.Errorf("Skipped() = %v, want only storageclasses forbidden", skipped)
	}
	if n := strings.Count(h.logs.String(), `msg="kind skipped"`); n != 1 {
		t.Errorf("kind skipped logged %d times, want 1:\n%s", n, h.logs.String())
	}
	want := []schema.GroupVersionResource{{Version: "v1", Resource: "configmaps"}, {Version: "v1", Resource: "nodes"}, podsGVR, deployGVR}
	if got := h.w.Watched(); !slices.Equal(got, want) {
		t.Errorf("Watched() = %v, want %v", got, want)
	}
}

// TestWatcherNeverReadsSecrets pins PRD R2: no list, watch or get of Secrets is ever sent,
// and none reaches the model, even though the core group is named and a Secret exists.
func TestWatcherNeverReadsSecrets(t *testing.T) {
	h := start(t, fixtures()...)
	for _, a := range h.dyn.Actions() {
		if a.GetResource().Resource == "secrets" {
			t.Errorf("Secrets were read: %s %s", a.GetVerb(), a.GetResource())
		}
	}
	if _, ok := h.rec.find(func(c model.Change) bool { _, ok := c.Obj.(*corev1.Secret); return ok }); ok {
		t.Error("a Secret reached the sink")
	}
}

func TestWatcherDeliversLiveChanges(t *testing.T) {
	h := start(t, fixtures()...)
	<-h.podWatch
	pod := modeltest.Pod("shop", "web-2", modeltest.Labels("app", "web"), nil)
	u, err := runtime.DefaultUnstructuredConverter.ToUnstructured(pod)
	if err != nil {
		t.Fatal(err)
	}
	obj := &unstructured.Unstructured{Object: u}
	obj.SetAPIVersion("v1")
	obj.SetKind("Pod")
	ctx := t.Context()
	if _, err := h.dyn.Resource(podsGVR).Namespace("shop").Create(ctx, obj, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	isWeb2 := func(deleted bool) func(model.Change) bool {
		return func(c model.Change) bool {
			p, ok := c.Obj.(*corev1.Pod)
			return ok && p.Name == "web-2" && c.Deleted == deleted
		}
	}
	waitFor(t, "added pod", func() bool { _, ok := h.rec.find(isWeb2(false)); return ok })
	if err := h.dyn.Resource(podsGVR).Namespace("shop").Delete(ctx, "web-2", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "deleted pod", func() bool { _, ok := h.rec.find(isWeb2(true)); return ok })
	waitFor(t, "idle queue", h.w.Synced)
}

func TestStartFailsWhenDiscoveryFails(t *testing.T) {
	disc := fakeDiscovery()
	disc.PrependReactor("get", "group", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("connection refused")
	})
	w := New(disc, dynamicfake.NewSimpleDynamicClient(scheme.Scheme), func([]model.Change) {}, slog.New(slog.DiscardHandler))
	if err := w.Start(t.Context()); err == nil {
		t.Fatal("Start succeeded with discovery down")
	}
}

// TestTransientListErrorKeepsKind: a list check that fails for a reason other than RBAC must
// not drop the kind; the informer is created and retries.
func TestTransientListErrorKeepsKind(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClient(scheme.Scheme)
	var failed sync.Once
	dyn.PrependReactor("list", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		handled := false
		failed.Do(func() { handled = true })
		if handled {
			return true, nil, apierrors.NewServiceUnavailable("etcd leader change")
		}
		return false, nil, nil
	})
	w := New(fakeDiscovery(), dyn, func([]model.Change) {}, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); w.Wait() })
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	for _, s := range w.Skipped() {
		if s.Resource == deployGVR {
			t.Fatalf("deployments skipped after a transient error: %v", s)
		}
	}
	waitFor(t, "sync after retry", w.Synced)
}
