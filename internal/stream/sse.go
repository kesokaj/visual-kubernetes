package stream

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

// DefaultHeartbeat is how often an idle stream sends an SSE comment, so port-forward and
// proxies keep the connection open.
const DefaultHeartbeat = 15 * time.Second

// handleStream serves `GET /api/stream`: the subscription's initial entries, then every later
// event, until the client goes away or the hub drops it (the browser then reconnects with
// Last-Event-ID and resumes from the ring).
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	sub, err := s.Hub.Subscribe(r.Header.Get("Last-Event-ID"))
	if err != nil {
		s.log().ErrorContext(r.Context(), "subscribe failed", "error", err.Error())
		http.Error(w, "stream unavailable", http.StatusInternalServerError)
		return
	}
	defer sub.Close()
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	for _, e := range sub.Initial {
		if writeEntry(w, e) != nil {
			return
		}
	}
	if rc.Flush() != nil {
		return
	}
	beat := s.Heartbeat
	if beat <= 0 {
		beat = DefaultHeartbeat
	}
	ticker := time.NewTicker(beat)
	defer ticker.Stop()
	for {
		var err error
		select {
		case <-r.Context().Done():
			return
		case e, ok := <-sub.C:
			if !ok {
				return
			}
			err = writeEntry(w, e)
		case <-ticker.C:
			_, err = io.WriteString(w, ": ping\n\n")
		}
		if err != nil || rc.Flush() != nil {
			return
		}
	}
}

// writeEntry writes one SSE event. The data is a single line of JSON (encoding/json never
// emits a raw newline), so one `data:` field carries it.
func writeEntry(w io.Writer, e Entry) error {
	_, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.ID, e.Type, e.Data)
	return err
}
