// Package watch discovers the kinds of the API groups the ClusterRole names (plan D16), skips
// Secrets and every kind the cluster forbids (logged once, never fatal), watches Events
// through one API only (D29), and runs one informer per remaining kind, handing each add,
// update and delete to a sink in batches from a single goroutine, so the sink never runs
// concurrently with itself.
package watch

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

// Sink receives batches of changes; the model's Apply in production.
type Sink func([]model.Change)

// Skip is a resource that is not watched, with the reason logged for it.
type Skip struct {
	Resource schema.GroupVersionResource
	Reason   string
}

// Watcher owns the informers and the goroutine that feeds the sink.
type Watcher struct {
	disc discovery.DiscoveryInterface
	dyn  dynamic.Interface
	sink Sink
	log  *slog.Logger
	q    queue
	wg   sync.WaitGroup

	mu       sync.Mutex
	started  bool // set once every informer is registered; Synced is false before
	watched  []schema.GroupVersionResource
	skipped  []Skip
	handlers []cache.ResourceEventHandlerRegistration
}

// New returns a Watcher; nothing runs until Start.
func New(disc discovery.DiscoveryInterface, dyn dynamic.Interface, sink Sink, log *slog.Logger) *Watcher {
	return &Watcher{disc: disc, dyn: dyn, sink: sink, log: log, q: queue{signal: make(chan struct{}, 1)}}
}

// Start discovers the resources, checks each can be listed, starts an informer per permitted
// resource and the sink goroutine, and returns. Everything stops when ctx is cancelled; Wait
// then returns once it has. Only a failed discovery is an error.
func (w *Watcher) Start(ctx context.Context) error {
	gvrs, partial, err := Resources(ctx, w.disc)
	if err != nil {
		return err
	}
	if partial != nil {
		w.log.Warn("discovery incomplete; those groups are not watched", "error", partial.Error())
	}
	factory := dynamicinformer.NewDynamicSharedInformerFactory(w.dyn, 0)
	for _, gvr := range w.oneEventAPI(ctx, gvrs) {
		if reason := w.check(ctx, gvr); reason != "" {
			w.skip(gvr, reason)
			continue
		}
		inf := factory.ForResource(gvr).Informer()
		if err := inf.SetTransform(trimForCache); err != nil { // only once the informer started
			return err
		}
		reg, err := inf.AddEventHandler(w.handler())
		if err != nil { // only when the informer already stopped, i.e. ctx is done
			return err
		}
		w.mu.Lock()
		w.watched = append(w.watched, gvr)
		w.handlers = append(w.handlers, reg)
		w.mu.Unlock()
	}
	factory.Start(ctx.Done())
	w.mu.Lock()
	w.started = true
	w.mu.Unlock()
	w.wg.Go(func() { w.pump(ctx) })
	w.wg.Go(func() {
		<-ctx.Done()
		factory.Shutdown()
	})
	return nil
}

// skip records a resource that is not watched and logs why: a warning, except for the core/v1
// Events that events.k8s.io already delivers.
func (w *Watcher) skip(gvr schema.GroupVersionResource, reason string) {
	w.mu.Lock()
	w.skipped = append(w.skipped, Skip{Resource: gvr, Reason: reason})
	slices.SortFunc(w.skipped, func(a, b Skip) int { return strings.Compare(a.Resource.String(), b.Resource.String()) })
	w.mu.Unlock()
	if reason == skipDuplicateEvents {
		w.log.Info("kind skipped", "resource", gvr.String(), "reason", reason)
		return
	}
	w.log.Warn("kind skipped", "resource", gvr.String(), "reason", reason)
}

// handler queues every add, delete and real update an informer delivers; relist updates that
// carry an unchanged object are dropped (sameVersion).
func (w *Watcher) handler() cache.ResourceEventHandlerFuncs {
	return cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) { w.enqueue(obj, false) },
		UpdateFunc: func(old, obj any) {
			if !sameVersion(old, obj) {
				w.enqueue(obj, false)
			}
		},
		DeleteFunc: func(obj any) { w.enqueue(obj, true) },
	}
}

// sameVersion reports whether an update carries an object the cache already had, as a relist
// delivers every unchanged object; passing those on would resend every cached Event.
func sameVersion(old, obj any) bool {
	o, ok1 := old.(*unstructured.Unstructured)
	n, ok2 := obj.(*unstructured.Unstructured)
	return ok1 && ok2 && n.GetResourceVersion() != "" && o.GetResourceVersion() == n.GetResourceVersion()
}

// check lists one item to learn whether the resource can be watched. Forbidden, gone and
// list-less resources are skipped; any other error leaves the informer to retry, because a
// transient failure must not drop a kind for the life of the process.
func (w *Watcher) check(ctx context.Context, gvr schema.GroupVersionResource) string {
	_, err := w.dyn.Resource(gvr).List(ctx, metav1.ListOptions{Limit: 1})
	switch {
	case err == nil:
		return ""
	case apierrors.IsForbidden(err):
		return "forbidden"
	case apierrors.IsNotFound(err):
		return "not found"
	case apierrors.IsMethodNotSupported(err):
		return "list not supported"
	default:
		w.log.Warn("list check failed; the informer will retry", "resource", gvr.String(), "error", err.Error())
		return ""
	}
}

// Wait blocks until the goroutines Start launched have exited after ctx was cancelled.
func (w *Watcher) Wait() { w.wg.Wait() }

// Synced reports whether Start has registered every informer, every watched kind delivered
// its initial list and the sink has applied every change queued so far (the `/healthz`
// condition; skipped kinds do not count). It is false before and during Start.
func (w *Watcher) Synced() bool {
	w.mu.Lock()
	started, handlers := w.started, w.handlers
	w.mu.Unlock()
	if !started {
		return false
	}
	for _, h := range handlers {
		if !h.HasSynced() {
			return false
		}
	}
	return w.q.idle()
}

// Watched returns the resources with an informer, sorted.
func (w *Watcher) Watched() []schema.GroupVersionResource {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]schema.GroupVersionResource(nil), w.watched...)
}

// Skipped returns the resources that are not watched and why, sorted by resource.
func (w *Watcher) Skipped() []Skip {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]Skip(nil), w.skipped...)
}

func (w *Watcher) enqueue(obj any, deleted bool) {
	if tomb, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tomb.Obj
	}
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return
	}
	w.q.push(model.Change{Obj: typed(u), Deleted: deleted})
}

// typed converts to client-go's typed object when the kind is built in, so the model can
// switch on Go types; CRD kinds (VPA, Gateway API) and failed conversions stay unstructured.
func typed(u *unstructured.Unstructured) runtime.Object {
	obj, err := scheme.Scheme.New(u.GroupVersionKind())
	if err != nil {
		return u
	}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, obj); err != nil {
		return u
	}
	return obj
}

func (w *Watcher) pump(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.q.signal:
		}
		for batch := w.q.take(); len(batch) > 0; batch = w.q.take() {
			w.sink(batch)
			w.q.done()
		}
	}
}

// queue is an unbounded FIFO of changes drained in whole batches, so the initial list of a
// large cluster reaches the sink in a few calls instead of one per object.
type queue struct {
	mu     sync.Mutex
	items  []model.Change
	busy   bool
	signal chan struct{}
}

func (q *queue) push(c model.Change) {
	q.mu.Lock()
	q.items = append(q.items, c)
	q.mu.Unlock()
	select {
	case q.signal <- struct{}{}:
	default: // a wake-up is already pending
	}
}

func (q *queue) take() []model.Change {
	q.mu.Lock()
	defer q.mu.Unlock()
	items := q.items
	q.items = nil
	q.busy = len(items) > 0
	return items
}

func (q *queue) done() {
	q.mu.Lock()
	q.busy = false
	q.mu.Unlock()
}

func (q *queue) idle() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items) == 0 && !q.busy
}
