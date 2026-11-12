package stream

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

// sseEvent is one parsed SSE event.
type sseEvent struct{ id, event, data string }

// readSSE reads n events (skipping comments) from an SSE body.
func readSSE(t *testing.T, r *bufio.Reader, n int) []sseEvent {
	t.Helper()
	var out []sseEvent
	var cur sseEvent
	for len(out) < n {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("reading stream after %d events: %v", len(out), err)
		}
		line = strings.TrimSuffix(line, "\n")
		switch {
		case line == "":
			if cur.event != "" {
				out = append(out, cur)
			}
			cur = sseEvent{}
		case strings.HasPrefix(line, "id: "):
			cur.id = line[4:]
		case strings.HasPrefix(line, "event: "):
			cur.event = line[7:]
		case strings.HasPrefix(line, "data: "):
			cur.data = line[6:]
		}
	}
	return out
}

func openStream(t *testing.T, url, lastID string) (*bufio.Reader, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/api/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	return bufio.NewReader(resp.Body), func() { cancel(); _ = resp.Body.Close() }
}

func TestStreamSnapshotThenLiveThenResume(t *testing.T) {
	h, _ := newTestHub(t, HubOptions{})
	ts := httptest.NewServer((&Server{Hub: h}).Handler())
	defer ts.Close()

	r, done := openStream(t, ts.URL, "")
	snap := readSSE(t, r, 1)[0]
	if snap.event != EventSnapshot || !strings.Contains(snap.data, `"core/Pod/ns/a"`) {
		t.Fatalf("first event = %+v, want the snapshot", snap)
	}
	publish(t, h, upsert("core/Pod/ns/b", "Running"))
	live := readSSE(t, r, 1)[0]
	if live.event != "upsert" || !strings.Contains(live.data, `"core/Pod/ns/b"`) {
		t.Fatalf("live event = %+v, want the upsert of b", live)
	}
	done()

	// While disconnected, two more events; a resume from the live id gets exactly those.
	publish(t, h, upsert("core/Pod/ns/c", "Pending"), model.Event{Type: string(model.OpDelete), Data: model.IDData{ID: "core/Pod/ns/a"}})
	r, done = openStream(t, ts.URL, live.id)
	defer done()
	missed := readSSE(t, r, 2)
	if missed[0].event != "upsert" || missed[1].event != "delete" {
		t.Fatalf("resume = %+v, want upsert then delete", missed)
	}
	liveID, _ := strconv.ParseUint(live.id, 10, 64)
	if missed[0].id != strconv.FormatUint(liveID+1, 10) {
		t.Errorf("resume first id = %s, want %d", missed[0].id, liveID+1)
	}
}

func TestStreamOldIDGetsSnapshot(t *testing.T) {
	h, _ := newTestHub(t, HubOptions{})
	ts := httptest.NewServer((&Server{Hub: h}).Handler())
	defer ts.Close()
	r, done := openStream(t, ts.URL, "42")
	defer done()
	if ev := readSSE(t, r, 1)[0]; ev.event != EventSnapshot {
		t.Fatalf("old Last-Event-ID got %+v, want a snapshot", ev)
	}
}

// fakeObjects answers for one id.
type fakeObjects struct{ id string }

func (f fakeObjects) Object(_ context.Context, ref model.Ref) ([]byte, error) {
	if ref.ID() != f.id {
		return nil, ErrNotFound
	}
	return []byte("kind: Pod\n"), nil
}

// fakeLogs records the request it was given.
type fakeLogs struct {
	tail      int64
	container string
	err       error
}

func (f *fakeLogs) Logs(_ context.Context, _ model.Ref, container string, tail int64) (io.ReadCloser, error) {
	f.tail, f.container = tail, container
	if f.err != nil {
		return nil, f.err
	}
	return io.NopCloser(strings.NewReader("line\n")), nil
}

func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func TestObjectsAndLogsStatus(t *testing.T) {
	logs := &fakeLogs{}
	h := (&Server{Hub: NewHub(HubOptions{}), Objects: fakeObjects{id: "core/Pod/ns/a"}, Logs: logs}).Handler()
	for _, tc := range []struct {
		target string
		code   int
	}{
		{"/api/objects?id=core/Pod/ns/a", 200},
		{"/api/objects?id=core/Pod/ns/zzz", 404},
		{"/api/objects?id=not-an-id", 400},
		{"/api/objects?id=cp/apiserver", 400},
		{"/api/objects", 400},
		{"/api/logs?id=core/Pod/ns/a", 200},
		{"/api/logs?id=apps/Deployment/ns/a", 400},
		{"/api/logs?id=core/Pod/ns/a&tailLines=0", 400},
		{"/api/logs?id=core/Pod/ns/a&tailLines=-3", 400},
		{"/api/logs?id=core/Pod/ns/a&tailLines=ten", 400},
	} {
		if rec := get(t, h, tc.target); rec.Code != tc.code {
			t.Errorf("GET %s = %d, want %d", tc.target, rec.Code, tc.code)
		} else if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("GET %s: missing nosniff", tc.target)
		}
	}
}

func TestLogsTailLines(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  int64
	}{
		{"", DefaultTailLines},
		{"&tailLines=5", 5},
		{"&tailLines=1000", 1000},
		{"&tailLines=5000", MaxTailLines},
	} {
		logs := &fakeLogs{}
		h := (&Server{Hub: NewHub(HubOptions{}), Logs: logs}).Handler()
		if rec := get(t, h, "/api/logs?id=core/Pod/ns/a&container=app"+tc.query); rec.Code != 200 {
			t.Fatalf("%q: status %d", tc.query, rec.Code)
		}
		if logs.tail != tc.want || logs.container != "app" {
			t.Errorf("%q: source got tail %d container %q, want %d app", tc.query, logs.tail, logs.container, tc.want)
		}
	}
}

func TestLogsUpstreamErrors(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
	}{
		{ErrNotFound, 404},
		{&StatusError{Code: 403, Msg: "logs forbidden"}, 403},
		{errors.New("dial tcp 10.0.0.1:443: secret detail"), 502},
	} {
		h := (&Server{Hub: NewHub(HubOptions{}), Logs: &fakeLogs{err: tc.err}}).Handler()
		rec := get(t, h, "/api/logs?id=core/Pod/ns/a")
		if rec.Code != tc.code || strings.Contains(rec.Body.String(), "secret detail") {
			t.Errorf("%v: %d %q, want %d without upstream detail", tc.err, rec.Code, rec.Body.String(), tc.code)
		}
	}
}

func TestLogsDisabledAnswers403(t *testing.T) {
	called := false
	logs := &fakeLogs{}
	h := (&Server{
		Hub:     NewHub(HubOptions{}),
		Objects: fakeObjects{id: "core/Pod/ns/a"},
		Logs: logSourceFunc(func(ctx context.Context, pod model.Ref, container string, tail int64) (io.ReadCloser, error) {
			called = true
			return logs.Logs(ctx, pod, container, tail)
		}),
		LogsDisabled: true,
	}).Handler()

	for _, target := range []string{
		"/api/logs?id=core/Pod/ns/a",
		"/api/logs",
	} {
		rec := get(t, h, target)
		if rec.Code != http.StatusForbidden {
			t.Errorf("GET %s = %d, want 403", target, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "logs are off on this install") {
			t.Errorf("GET %s body = %q, want logs-off note", target, rec.Body.String())
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("GET %s: missing nosniff", target)
		}
	}
	if called {
		t.Error("LogSource called while LogsDisabled is true")
	}
	if rec := get(t, h, "/api/objects?id=core/Pod/ns/a"); rec.Code != http.StatusOK {
		t.Errorf("GET /api/objects with LogsDisabled = %d, want 200", rec.Code)
	}
}

type logSourceFunc func(context.Context, model.Ref, string, int64) (io.ReadCloser, error)

func (f logSourceFunc) Logs(ctx context.Context, pod model.Ref, container string, tail int64) (io.ReadCloser, error) {
	return f(ctx, pod, container, tail)
}

func TestHealthz(t *testing.T) {
	ready := false
	h := (&Server{Hub: NewHub(HubOptions{}), Ready: func() bool { return ready }}).Handler()
	if rec := get(t, h, "/healthz"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("healthz before sync = %d, want 503", rec.Code)
	}
	ready = true
	if rec := get(t, h, "/healthz"); rec.Code != http.StatusOK || rec.Body.String() != "ok\n" {
		t.Errorf("healthz after sync = %d %q, want 200 ok", rec.Code, rec.Body.String())
	}
}

func TestDemoMountDefaultClosedAndMounted(t *testing.T) {
	hub := NewHub(HubOptions{})
	publish(t, hub, model.Event{
		Type: EventSnapshot,
		Data: model.SnapshotData{
			Entities: []model.Entity{{ID: "core/Service/shop/web", Kind: "Service"}},
			Edges:    []model.Edge{{ID: "e1", From: "a", To: "b", Kind: model.EdgeRoutes}},
		},
	})
	ents, edges := hub.Snapshot()
	if len(ents) != 1 || ents[0].ID != "core/Service/shop/web" || len(edges) != 1 {
		t.Fatalf("Hub.Snapshot() = %v, %v", ents, edges)
	}

	off := (&Server{Hub: hub, Ready: func() bool { return true }}).Handler()
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, path := range []string{"/api/demo", "/api/demo/ports", "/api/demo/proxy/t/abc/"} {
			req := httptest.NewRequest(method, path, nil)
			rec := httptest.NewRecorder()
			off.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "demo mode is off on this install") {
				t.Errorf("%s %s = %d %q, want 403 demo-off", method, path, rec.Code, rec.Body.String())
			}
			if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Errorf("%s %s: missing nosniff", method, path)
			}
		}
	}

	on := (&Server{
		Hub:   hub,
		Ready: func() bool { return true },
		Demo: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			plain(w, http.StatusOK, "mounted:"+r.URL.Path)
		}),
	}).Handler()
	if rec := get(t, on, "/api/demo/ports"); rec.Code != http.StatusOK || rec.Body.String() != "mounted:/api/demo/ports" {
		t.Errorf("mounted /api/demo/ports = %d %q", rec.Code, rec.Body.String())
	}
}
