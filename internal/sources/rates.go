package sources

import "time"

// reading is one pod's cumulative network counters at the kubelet's sample time.
type reading struct {
	t      time.Time
	rx, tx uint64
}

// history is the readings of one pod inside the rate window, oldest first.
type history struct {
	uid      string
	rs       []reading
	lastSeen time.Time
}

// rateTable turns cumulative kubelet byte counters into bytes per second over a sliding window.
type rateTable struct {
	window time.Duration
	h      map[string]*history
}

// observe adds a reading for the pod id. A new pod UID (same name, new pod) or a counter that
// went backwards (container restart resets the sandbox counters) starts the history again; a
// reading whose time did not advance is the kubelet's cached sample and adds nothing.
func (rt *rateTable) observe(id, uid string, r reading, now time.Time) {
	h := rt.h[id]
	if h == nil || h.uid != uid {
		h = &history{uid: uid}
		rt.h[id] = h
	}
	h.lastSeen = now
	if n := len(h.rs); n > 0 {
		last := h.rs[n-1]
		if r.rx < last.rx || r.tx < last.tx {
			h.rs = h.rs[:0]
		} else if !r.t.After(last.t) {
			return
		}
	}
	h.rs = append(h.rs, r)
	// Keep the newest reading at or before the window start, so the rate spans the whole window
	// once enough readings exist.
	cut := r.t.Add(-rt.window)
	for len(h.rs) > 2 && !h.rs[1].t.After(cut) {
		h.rs = h.rs[1:]
	}
}

// rate returns the pod's receive and transmit bytes per second; ok is false until two readings
// with distinct times exist.
func (rt *rateTable) rate(id string) (rx, tx float64, ok bool) {
	h := rt.h[id]
	if h == nil || len(h.rs) < 2 {
		return 0, 0, false
	}
	first, last := h.rs[0], h.rs[len(h.rs)-1]
	secs := last.t.Sub(first.t).Seconds()
	if secs <= 0 {
		return 0, 0, false
	}
	return float64(last.rx-first.rx) / secs, float64(last.tx-first.tx) / secs, true
}

// prune forgets pods not seen for three windows: deleted pods, and pods on a node whose kubelet
// stopped answering, whose last rate would otherwise be shown as current.
func (rt *rateTable) prune(now time.Time) {
	for id, h := range rt.h {
		if now.Sub(h.lastSeen) > 3*rt.window {
			delete(rt.h, id)
		}
	}
}
