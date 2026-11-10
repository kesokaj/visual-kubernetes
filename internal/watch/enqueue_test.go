package watch

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

func unstructuredPod(name string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("v1")
	u.SetKind("Pod")
	u.SetNamespace("shop")
	u.SetName(name)
	return u
}

func TestEnqueueUnwrapsTombstone(t *testing.T) {
	w := New(nil, nil, nil, nil)
	w.enqueue(cache.DeletedFinalStateUnknown{Key: "shop/web-1", Obj: unstructuredPod("web-1")}, true)
	w.enqueue("not an object", false)
	batch := w.q.take()
	if len(batch) != 1 {
		t.Fatalf("queued %d changes, want 1", len(batch))
	}
	p, ok := batch[0].Obj.(*corev1.Pod)
	if !ok || p.Name != "web-1" || !batch[0].Deleted {
		t.Errorf("queued %T %+v, want deleted *corev1.Pod web-1", batch[0].Obj, batch[0])
	}
}

func TestTypedKeepsUnknownKindsUnstructured(t *testing.T) {
	vpa := &unstructured.Unstructured{}
	vpa.SetAPIVersion("autoscaling.k8s.io/v1")
	vpa.SetKind("VerticalPodAutoscaler")
	vpa.SetName("web")
	if got := typed(vpa); got != vpa {
		t.Errorf("typed(VPA) = %T, want the same unstructured object", got)
	}
	bad := unstructuredPod("web-1")
	bad.Object["spec"] = "not a map"
	if got := typed(bad); got != bad {
		t.Errorf("typed(malformed Pod) = %T, want the unstructured object back", got)
	}
}

func TestQueueIdleTracksInFlightBatch(t *testing.T) {
	q := queue{signal: make(chan struct{}, 1)}
	if !q.idle() {
		t.Fatal("new queue not idle")
	}
	q.push(model.Change{})
	q.push(model.Change{})
	if q.idle() {
		t.Fatal("queue with items reported idle")
	}
	if n := len(q.take()); n != 2 {
		t.Fatalf("take() = %d items, want 2", n)
	}
	if q.idle() {
		t.Fatal("queue idle while a batch is being applied")
	}
	q.done()
	if !q.idle() {
		t.Fatal("queue not idle after done")
	}
	if len(q.signal) != 1 {
		t.Fatalf("pending wake-ups = %d, want exactly 1", len(q.signal))
	}
}

func TestSyncedFalseBeforeStart(t *testing.T) {
	if New(nil, nil, nil, nil).Synced() {
		t.Error("Synced() = true before Start registered any informer")
	}
}
