package stream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

// Recording is a loaded recorded stream file (plan D14): NDJSON records whose first is a
// snapshot at atMs 0 and whose atMs never decreases.
type Recording struct {
	steps []step // records grouped by atMs
}

type step struct {
	atMs   int64
	events []model.Event
}

type rawRecord struct {
	AtMs  *int64          `json:"atMs"`
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

// LoadRecording reads and validates a recorded stream. State-changing events are decoded into
// the model's types so the hub's view follows them; other types pass through as raw JSON.
func LoadRecording(r io.Reader) (*Recording, error) {
	dec := json.NewDecoder(r)
	rec := &Recording{}
	for n := 1; ; n++ {
		var raw rawRecord
		err := dec.Decode(&raw)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("record %d: %w", n, err)
		}
		ev, err := decodeRecord(raw, n)
		if err != nil {
			return nil, err
		}
		at := *raw.AtMs
		switch last := len(rec.steps) - 1; {
		case last >= 0 && at < rec.steps[last].atMs:
			return nil, fmt.Errorf("record %d: atMs %d goes back from %d", n, at, rec.steps[last].atMs)
		case last >= 0 && at == rec.steps[last].atMs:
			rec.steps[last].events = append(rec.steps[last].events, ev)
		default:
			rec.steps = append(rec.steps, step{atMs: at, events: []model.Event{ev}})
		}
	}
	if len(rec.steps) == 0 {
		return nil, errors.New("empty recording")
	}
	return rec, nil
}

func decodeRecord(raw rawRecord, n int) (model.Event, error) {
	if raw.AtMs == nil || *raw.AtMs < 0 {
		return model.Event{}, fmt.Errorf("record %d: missing or negative atMs", n)
	}
	if !validEventName(raw.Event) {
		return model.Event{}, fmt.Errorf("record %d: event name %q is not a plain word", n, raw.Event)
	}
	if n == 1 && (raw.Event != EventSnapshot || *raw.AtMs != 0) {
		return model.Event{}, fmt.Errorf("record 1: want a snapshot at atMs 0, got %s at %d", raw.Event, *raw.AtMs)
	}
	var data any
	switch raw.Event {
	case EventSnapshot:
		data = &model.SnapshotData{}
	case string(model.OpUpsert):
		data = &model.EntityData{}
	case string(model.OpEdgeUpsert):
		data = &model.EdgeData{}
	case string(model.OpDelete), string(model.OpEdgeDelete):
		data = &model.IDData{}
	case EventCapabilities:
		data = &CapabilitiesData{}
	default:
		if !json.Valid(raw.Data) {
			return model.Event{}, fmt.Errorf("record %d: %s data is not JSON", n, raw.Event)
		}
		return model.Event{Type: raw.Event, Data: raw.Data}, nil
	}
	if err := json.Unmarshal(raw.Data, data); err != nil {
		return model.Event{}, fmt.Errorf("record %d: %s data: %w", n, raw.Event, err)
	}
	return model.Event{Type: raw.Event, Data: deref(data)}, nil
}

// deref turns the decode target back into the value type the view switches on.
func deref(v any) any {
	switch d := v.(type) {
	case *model.SnapshotData:
		return *d
	case *model.EntityData:
		return *d
	case *model.EdgeData:
		return *d
	case *model.IDData:
		return *d
	case *CapabilitiesData:
		return *d
	}
	return v
}

// validEventName keeps SSE framing intact: an event name is written verbatim after `event:`.
func validEventName(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}
	return true
}

// Play publishes the snapshot at once (the hub's view, and so /healthz and every snapshot,
// start from it), waits for the first subscriber, then publishes each later step at its
// offset from that moment. It plays once and returns; it returns early when ctx ends.
func (rec *Recording) Play(ctx context.Context, h *Hub) error {
	if err := h.Publish(rec.steps[0].events...); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return nil
	case <-h.Subscribed():
	}
	start := time.Now()
	for _, st := range rec.steps[1:] {
		t := time.NewTimer(time.Until(start.Add(time.Duration(st.atMs) * time.Millisecond)))
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-t.C:
		}
		if err := h.Publish(st.events...); err != nil {
			return err
		}
	}
	return nil
}

// ReplayObjects serves `/api/objects` in replay mode, where no cluster exists: the entity as
// the recording carries it, as YAML; ids not in the view are 404.
type ReplayObjects struct{ Hub *Hub }

// Object implements ObjectSource.
func (r ReplayObjects) Object(_ context.Context, ref model.Ref) ([]byte, error) {
	e, ok := r.Hub.Entity(ref.ID())
	if !ok {
		return nil, ErrNotFound
	}
	return yaml.Marshal(e)
}

// ReplayLogs serves `/api/logs` in replay mode: recordings carry no logs, so a known pod gets
// a one-line note and an unknown one 404.
type ReplayLogs struct{ Hub *Hub }

// ReplayLogNote is the body ReplayLogs returns for a pod in the recording.
const ReplayLogNote = "replay mode: logs are not recorded\n"

// Logs implements LogSource.
func (r ReplayLogs) Logs(_ context.Context, ref model.Ref, _ string, _ int64) (io.ReadCloser, error) {
	if _, ok := r.Hub.Entity(ref.ID()); !ok {
		return nil, ErrNotFound
	}
	return io.NopCloser(strings.NewReader(ReplayLogNote)), nil
}
