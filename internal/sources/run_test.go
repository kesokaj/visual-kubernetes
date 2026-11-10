package sources

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	"github.com/kesokaj/visual-kubernetes/internal/probe"
)

// recorder is a Publisher that counts the cycles it receives.
type recorder struct {
	mu     sync.Mutex
	cycles int
}

func (r *recorder) Publish(...model.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cycles++
	return nil
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cycles
}

// TestRunWaitsForReady: before the informers sync the model is empty, so a cycle would publish
// false states (no nodes, no leader leases). Run must publish nothing until Ready, then every
// interval, even when Ready later reports false.
func TestRunWaitsForReady(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var ready atomic.Bool
		out := &recorder{}
		c := New(Deps{Model: cluster(t), Raw: healthyRaw(), KubeletErr: probe.ErrLocalMode, Out: out, Ready: ready.Load})
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { c.Run(ctx, time.Second); close(done) }()
		time.Sleep(2500 * time.Millisecond)
		synctest.Wait()
		if n := out.count(); n != 0 {
			t.Fatalf("published %d cycles before Ready, want 0", n)
		}
		ready.Store(true)
		time.Sleep(2 * time.Second) // ticks at 3 s and 4 s
		synctest.Wait()
		if n := out.count(); n != 2 {
			t.Errorf("published %d cycles after Ready, want 2", n)
		}
		// Synced drops again while informer changes queue; the gate must stay open.
		ready.Store(false)
		time.Sleep(2 * time.Second) // ticks at 5 s and 6 s
		synctest.Wait()
		if n := out.count(); n != 4 {
			t.Errorf("published %d cycles after Ready dropped, want 4 (gate latches)", n)
		}
		cancel()
		<-done
	})
}
