package stream

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

// Limits on `/api/logs` (plan Interfaces § HTTP).
const (
	DefaultTailLines = 200
	MaxTailLines     = 1000
	// MaxLogBytes caps one logs response, whatever the line count.
	MaxLogBytes = 1 << 20
	// LogsDisabledMessage is the 403 body when --logs=false (plan D38).
	LogsDisabledMessage = "logs are off on this install"
	// DemoDisabledMessage is the 403 body when --demo=false (ADR-010).
	DemoDisabledMessage = "demo mode is off on this install"
)

// ErrNotFound is what an ObjectSource or LogSource returns for an id that is not in the view
// (or a container the pod does not have); the handlers answer 404.
var ErrNotFound = errors.New("not found")

// ObjectSource returns an object as YAML for `/api/objects`. ref is already a valid D13 object
// id; the source must look it up in the model before any API call.
type ObjectSource interface {
	Object(ctx context.Context, ref model.Ref) ([]byte, error)
}

// LogSource streams a pod's logs for `/api/logs`. container may be empty (the first
// container); tailLines is already within 1..MaxTailLines.
type LogSource interface {
	Logs(ctx context.Context, pod model.Ref, container string, tailLines int64) (io.ReadCloser, error)
}

// StatusError lets a source choose the HTTP status for an upstream failure (a 403 from the API
// server), with a message safe to show.
type StatusError struct {
	Code int
	Msg  string
}

func (e *StatusError) Error() string { return e.Msg }

// Server wires the hub and the sources to HTTP.
type Server struct {
	Hub          *Hub
	Ready        func() bool // the /healthz condition
	Objects      ObjectSource
	Logs         LogSource
	LogsDisabled bool         // --logs=false: /api/logs answers 403 (D38, ADR-008)
	Demo         http.Handler // optional --demo handler mounted at /api/demo/; nil answers 403
	Log          *slog.Logger
	// Heartbeat is the idle-stream comment interval; DefaultHeartbeat when zero.
	Heartbeat time.Duration
}

// Handler returns the mux: `/healthz`, `/api/stream`, `/api/objects`, `/api/logs`, `/api/demo/*`,
// and a placeholder `/` until the embedded page lands (plan Phase 3).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /api/stream", s.handleStream)
	mux.HandleFunc("GET /api/objects", s.handleObjects)
	mux.HandleFunc("GET /api/logs", s.handleLogs)
	demoHandler := s.Demo
	if demoHandler == nil {
		demoHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			plain(w, http.StatusForbidden, DemoDisabledMessage+"\n")
		})
	}
	mux.Handle("/api/demo/", demoHandler)
	mux.Handle("/api/demo", demoHandler)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		plain(w, http.StatusOK, "visual-kubernetes: the page arrives with the UI; the stream is at /api/stream\n")
	})
	return mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	if s.Ready == nil || !s.Ready() {
		plain(w, http.StatusServiceUnavailable, "syncing\n")
		return
	}
	plain(w, http.StatusOK, "ok\n")
}

func (s *Server) handleObjects(w http.ResponseWriter, r *http.Request) {
	ref, err := model.ParseID(r.URL.Query().Get("id"))
	if err != nil {
		plain(w, http.StatusBadRequest, "malformed id\n")
		return
	}
	body, err := s.Objects.Object(r.Context(), ref)
	if err != nil {
		s.fail(w, r, "object", err)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(body) // the status is sent; a client that left cannot be told
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	if s.LogsDisabled || s.Logs == nil {
		plain(w, http.StatusForbidden, LogsDisabledMessage+"\n")
		return
	}
	q := r.URL.Query()
	ref, err := model.ParseID(q.Get("id"))
	if err != nil || ref.Group != model.CoreGroup || ref.Kind != "Pod" {
		plain(w, http.StatusBadRequest, "id must be a pod id: core/Pod/<namespace>/<name>\n")
		return
	}
	tail, ok := tailLines(q.Get("tailLines"))
	if !ok {
		plain(w, http.StatusBadRequest, "tailLines must be a positive integer\n")
		return
	}
	rc, err := s.Logs.Logs(r.Context(), ref, q.Get("container"), tail)
	if err != nil {
		s.fail(w, r, "logs", err)
		return
	}
	defer func() { _ = rc.Close() }() // read-only stream; a close error changes nothing
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if _, err := io.Copy(w, io.LimitReader(rc, MaxLogBytes)); err != nil {
		s.log().WarnContext(r.Context(), "logs copy ended early", "error", err.Error())
	}
}

// tailLines parses the tailLines parameter: absent means the default, above the cap means the
// cap, anything not a positive integer is rejected.
func tailLines(v string) (int64, bool) {
	if v == "" {
		return DefaultTailLines, true
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 1 {
		return 0, false
	}
	return min(n, MaxTailLines), true
}

// fail maps a source error to a status without echoing upstream detail to the client.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, what string, err error) {
	var se *StatusError
	switch {
	case errors.Is(err, ErrNotFound):
		plain(w, http.StatusNotFound, what+" not found\n")
	case errors.As(err, &se):
		s.log().WarnContext(r.Context(), what+" request refused upstream", "path", r.URL.Path, "status", se.Code)
		plain(w, se.Code, se.Msg+"\n")
	default:
		s.log().ErrorContext(r.Context(), what+" request failed", "path", r.URL.Path, "error", err.Error())
		plain(w, http.StatusBadGateway, what+" unavailable\n")
	}
}

func (s *Server) log() *slog.Logger {
	if s.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return s.Log
}

func plain(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	_, _ = io.WriteString(w, msg) // best effort: the status already tells the client
}
