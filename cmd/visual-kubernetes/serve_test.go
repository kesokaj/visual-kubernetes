package main

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/kesokaj/visual-kubernetes/internal/hubble"
	"github.com/kesokaj/visual-kubernetes/internal/model"
	"github.com/kesokaj/visual-kubernetes/internal/stream"
)

func discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

func TestListenAddr(t *testing.T) {
	for _, tc := range []struct {
		flag      string
		inCluster bool
		want      string
	}{
		{"", false, "127.0.0.1:8080"}, // the operator's credentials stay on loopback (D26)
		{"", true, ":8080"},
		{":9090", false, ":9090"},
	} {
		if got := listenAddr(tc.flag, tc.inCluster); got != tc.want {
			t.Errorf("listenAddr(%q, %v) = %q, want %q", tc.flag, tc.inCluster, got, tc.want)
		}
	}
}

// TestReplayServes: --replay serves /healthz and a snapshot from the fixture with no cluster.
func TestReplayServes(t *testing.T) {
	a, err := startReplay(t.Context(), filepath.Join("..", "..", "test", "fixtures", "streams", "deploy-scale.ndjson"), discard())
	if err != nil {
		t.Fatal(err)
	}
	if a.inCluster {
		t.Error("replay app claims to be in a cluster")
	}
	ts := httptest.NewServer(a.handler)
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz = %d, want 200", resp.StatusCode)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/stream", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	r := bufio.NewReader(resp.Body)
	_, _ = r.ReadString('\n') // id
	if line, _ := r.ReadString('\n'); line != "event: snapshot\n" {
		t.Errorf("second line = %q, want the snapshot event", line)
	}
}

func TestReplayRejectsBadFile(t *testing.T) {
	if _, err := startReplay(t.Context(), filepath.Join(t.TempDir(), "absent.ndjson"), discard()); err == nil {
		t.Error("missing recording accepted")
	}
}

// TestServeHTTPShutdownEndsStreams: an open SSE stream must not hold shutdown for its timeout.
func TestServeHTTPShutdownEndsStreams(t *testing.T) {
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	opened := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(opened)
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- serveHTTP(ctx, ln, h) }()
	resp, err := http.Get("http://" + ln.Addr().String() + "/api/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	<-opened
	start := time.Now()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveHTTP = %v, want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown waited on the open stream")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("shutdown took %v", d)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
}

func TestRunRejectsStrayArgs(t *testing.T) {
	var stderr strings.Builder
	if code := run(t.Context(), []string{"--replay", "x.ndjson", "--kubeconfig", "k"}, io.Discard, &stderr, discard()); code != 2 {
		t.Errorf("--replay with --kubeconfig = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "usage:") {
		t.Errorf("stderr = %q, want the usage", stderr.String())
	}
}

// TestAPIWarningsOncePerText: client-go repeats deprecation warnings on every relist; the handler
// must log each distinct one once, as JSON, and ignore non-299 codes and empty text.
func TestAPIWarningsOncePerText(t *testing.T) {
	var buf strings.Builder
	w := &apiWarnings{log: slog.New(slog.NewJSONHandler(&buf, nil))}
	for range 3 {
		w.HandleWarningHeader(299, "", "v1 Endpoints is deprecated")
	}
	w.HandleWarningHeader(299, "", "another")
	w.HandleWarningHeader(199, "", "not a warning code")
	w.HandleWarningHeader(299, "", "")
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d log lines, want 2:\n%s", len(lines), buf.String())
	}
	if !strings.HasPrefix(lines[0], "{") || !strings.Contains(lines[0], `"text":"v1 Endpoints is deprecated"`) {
		t.Errorf("first line = %s, want JSON with the warning text", lines[0])
	}
}

// lockedBuffer is a log sink the test reads while the server writes to it.
type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// TestRunServeRefusesForeignHost drives the real runServe, so it fails if the HostGuard is
// ever unwired: a DNS-rebinding page sends its own name as Host and must get 403, while the
// loopback address keeps working.
func TestRunServeRefusesForeignHost(t *testing.T) {
	var logs lockedBuffer
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- runServe(ctx, serveOptions{addr: "127.0.0.1:0",
			replay: filepath.Join("..", "..", "test", "fixtures", "streams", "deploy-scale.ndjson")},
			slog.New(slog.NewJSONHandler(&logs, nil)))
	}()
	defer func() { cancel(); <-done }()
	var addr string
	for deadline := time.Now().Add(5 * time.Second); addr == "" && time.Now().Before(deadline); {
		if _, rest, ok := strings.Cut(logs.String(), `"msg":"serving","addr":"`); ok {
			addr, _, _ = strings.Cut(rest, `"`)
		} else {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if addr == "" {
		t.Fatalf("server never logged its address: %s", logs.String())
	}
	for host, want := range map[string]int{"": http.StatusOK, "evil.example": http.StatusForbidden} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/healthz", nil)
		if host != "" {
			req.Host = host
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body) // an unread body keeps the conn active and stalls Shutdown 5 s
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("Host %q: status %d, want %d", host, resp.StatusCode, want)
		}
	}
}

func TestStartHubbleLocalVsInCluster(t *testing.T) {
	cs := fake.NewSimpleClientset()
	m := model.New()
	hub := stream.NewHub(stream.HubOptions{})
	if err := hub.Publish(m.SnapshotEvent(map[string]model.Capability{})); err != nil {
		t.Fatal(err)
	}
	sub, err := hub.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	// Outside a cluster, startHubble must not touch the client or publish capabilities.
	startHubble(t.Context(), false, cs, m, hub, func() bool { return true }, discard())
	if acts := cs.Actions(); len(acts) != 0 {
		t.Fatalf("local mode made %d API calls, want 0", len(acts))
	}

	// In-cluster, startHubble detects the absent relay and publishes the missing capability.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	startHubble(ctx, true, cs, m, hub, func() bool { return true }, discard())
	select {
	case ev := <-sub.C:
		if ev.Type != stream.EventCapabilities || !strings.Contains(string(ev.Data), hubble.SourceName) {
			t.Errorf("in-cluster event = %s %s, want hubble-relay capability", ev.Type, ev.Data)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("in-cluster startHubble published no capability")
	}
}
