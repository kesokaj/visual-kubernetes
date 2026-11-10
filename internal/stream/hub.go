// Package stream serves the live view over HTTP: the SSE stream with Last-Event-ID resume
// from a 15 min ring buffer, `/healthz`, `/api/objects` and `/api/logs`, and the `--replay`
// player that feeds a recorded NDJSON stream into the same hub (plan Interfaces § HTTP, D14).
package stream

import (
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

// Defaults for HubOptions.
const (
	DefaultRetention = 15 * time.Minute
	DefaultMaxEvents = 50_000
	defaultSubBuffer = 1024
)

// Entry is one published event with its SSE id and its data already encoded as JSON.
type Entry struct {
	ID   uint64
	Type string
	Data []byte
	at   time.Time
}

// HubOptions configure a Hub; zero values take the defaults.
type HubOptions struct {
	// Retention is how long published events stay resumable (the plan's 15 min ring).
	Retention time.Duration
	// MaxEvents bounds the ring so a burst cannot grow memory without limit; a resume older
	// than what the ring holds gets a snapshot, as an expired one does.
	MaxEvents int
	// SubBuffer is each subscriber's queue; a subscriber that falls this far behind is
	// dropped and reconnects with Last-Event-ID.
	SubBuffer int
	// Now is the clock; time.Now when nil.
	Now func() time.Time
}

// Hub orders every published event, keeps the view and the ring buffer in step with the event
// ids, and fans events out to subscribers. All methods are safe for concurrent use.
type Hub struct {
	retention time.Duration
	maxEvents int
	subBuffer int
	now       func() time.Time

	mu       sync.Mutex
	last     uint64 // id of the newest event; ids start above any earlier process's ids
	ring     []Entry
	view     view
	subs     map[*Subscription]struct{}
	firstSub chan struct{} // closed on the first Subscribe
}

// NewHub returns an empty hub. Event ids start at the start time in microseconds, so ids from
// an earlier run of the binary (a browser resuming after a restart) are always older than the
// ring and get a snapshot instead of another process's events.
func NewHub(o HubOptions) *Hub {
	h := &Hub{
		retention: o.Retention, maxEvents: o.MaxEvents, subBuffer: o.SubBuffer, now: o.Now,
		view: newView(), subs: map[*Subscription]struct{}{}, firstSub: make(chan struct{}),
	}
	if h.retention <= 0 {
		h.retention = DefaultRetention
	}
	if h.maxEvents <= 0 {
		h.maxEvents = DefaultMaxEvents
	}
	if h.subBuffer <= 0 {
		h.subBuffer = defaultSubBuffer
	}
	if h.now == nil {
		h.now = time.Now
	}
	h.last = uint64(h.now().UnixMicro())
	return h
}

// Publish encodes the events, applies them to the view, gives each the next id, appends them
// to the ring and hands them to every subscriber, all in one step under the hub's lock. It
// returns an error, publishing nothing, when an event cannot be encoded.
func (h *Hub) Publish(events ...model.Event) error {
	if len(events) == 0 {
		return nil
	}
	encoded := make([][]byte, len(events))
	for i, ev := range events {
		b, err := json.Marshal(ev.Data)
		if err != nil {
			return fmt.Errorf("encoding %s event: %w", ev.Type, err)
		}
		encoded[i] = b
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	for i, ev := range events {
		h.view.apply(ev)
		h.last++
		e := Entry{ID: h.last, Type: ev.Type, Data: encoded[i], at: now}
		h.ring = append(h.ring, e)
		h.fanOut(e)
	}
	h.trim(now)
	return nil
}

// fanOut queues e for every subscriber, dropping any whose queue is full; the caller holds mu.
func (h *Hub) fanOut(e Entry) {
	for s := range h.subs {
		select {
		case s.ch <- e:
		default:
			delete(h.subs, s)
			close(s.ch)
		}
	}
}

// trim drops events older than the retention and the oldest beyond MaxEvents; the caller
// holds mu.
func (h *Hub) trim(now time.Time) {
	cut := 0
	for cut < len(h.ring) && (now.Sub(h.ring[cut].at) > h.retention || len(h.ring)-cut > h.maxEvents) {
		cut++
	}
	if cut > 0 {
		// Reslicing is amortised by append's reallocation; clearing the dropped entries lets
		// their data go before that happens.
		clear(h.ring[:cut])
		h.ring = h.ring[cut:]
	}
}

// Subscription is one client's place in the stream: Initial holds what the client must apply
// first (a snapshot, or the events it missed), then C delivers every later event. C is closed
// when the hub drops a subscriber that fell behind.
type Subscription struct {
	Initial []Entry
	C       <-chan Entry
	ch      chan Entry
	hub     *Hub
}

// Subscribe registers a subscriber. A lastEventID inside the ring (or equal to the newest id)
// resumes with only the events after it; anything else — absent, malformed, from an earlier
// process, older than the ring — gets a snapshot carrying the newest id.
func (h *Hub) Subscribe(lastEventID string) (*Subscription, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := &Subscription{ch: make(chan Entry, h.subBuffer), hub: h}
	s.C = s.ch
	if missed, ok := h.since(lastEventID); ok {
		s.Initial = missed
	} else {
		b, err := json.Marshal(h.view.snapshot())
		if err != nil {
			return nil, fmt.Errorf("encoding snapshot: %w", err)
		}
		s.Initial = []Entry{{ID: h.last, Type: EventSnapshot, Data: b}}
	}
	h.subs[s] = struct{}{}
	select {
	case <-h.firstSub:
	default:
		close(h.firstSub)
	}
	return s, nil
}

// since returns the ring events after lastEventID and whether the resume is possible; the
// caller holds mu.
func (h *Hub) since(lastEventID string) ([]Entry, bool) {
	id, err := strconv.ParseUint(lastEventID, 10, 64)
	if err != nil {
		return nil, false
	}
	oldest := h.last + 1 // with an empty ring only the newest id resumes
	if len(h.ring) > 0 {
		oldest = h.ring[0].ID
	}
	if id+1 < oldest || id > h.last {
		return nil, false
	}
	missed := h.ring[len(h.ring)-int(h.last-id):]
	return append([]Entry(nil), missed...), true
}

// Close unregisters the subscription; it is safe to call more than once.
func (s *Subscription) Close() {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	if _, ok := s.hub.subs[s]; ok {
		delete(s.hub.subs, s)
		close(s.ch)
	}
}

// Subscribed is closed once the first client has subscribed; the replay player waits on it so
// a recording plays from its start for the first viewer.
func (h *Hub) Subscribed() <-chan struct{} { return h.firstSub }

// Entity returns the view's entity with the given id.
func (h *Hub) Entity(id string) (model.Entity, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e, ok := h.view.entities[id]
	return e, ok
}
