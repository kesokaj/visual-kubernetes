package demo

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/net/websocket"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

type fakePodExec struct {
	model     *model.Model
	onExec    func(ctx context.Context, req ExecRequest) error
	lastSizes []remotecommand.TerminalSize
	mu        sync.Mutex
}

func (f *fakePodExec) ResolvePod(ref model.Ref, container string) (PodTarget, *StatusErr) {
	return (&KubeExec{Model: f.model}).ResolvePod(ref, container)
}

func (f *fakePodExec) Exec(ctx context.Context, req ExecRequest) error {
	if f.onExec != nil {
		return f.onExec(ctx, req)
	}
	go func() {
		for sz := req.SizeQueue.Next(); sz != nil; sz = req.SizeQueue.Next() {
			f.mu.Lock()
			f.lastSizes = append(f.lastSizes, *sz)
			f.mu.Unlock()
		}
	}()
	sc := bufio.NewScanner(req.Stdin)
	for sc.Scan() {
		line := sc.Text()
		if line == "exit" {
			return nil
		}
		_, _ = io.WriteString(req.Stdout, "out:"+line+"\n")
	}
	return ctx.Err()
}

func seedExecModel(t *testing.T) *model.Model {
	t.Helper()
	m := model.New()
	m.Apply(
		model.Change{
			Obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "web-0", UID: "pod-web-0"},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "web"}, {Name: "sidecar"}},
				},
				Status: corev1.PodStatus{Phase: corev1.PodRunning},
			},
		},
		model.Change{
			Obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "pending-0", UID: "pod-pending-0"},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "web"}}},
				Status:     corev1.PodStatus{Phase: corev1.PodPending},
			},
		},
		model.Change{
			Obj: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: "coredns-0", UID: "pod-coredns-0"},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "coredns"}}},
				Status:     corev1.PodStatus{Phase: corev1.PodRunning},
			},
		},
	)
	return m
}

func dialTestExecWS(t *testing.T, srvURL, query, origin string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(srvURL, "http") + "/api/demo/exec?" + query
	cfg, err := websocket.NewConfig(wsURL, origin)
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	ws, err := websocket.DialConfig(cfg)
	if err != nil {
		t.Fatalf("DialConfig(%s): %v", wsURL, err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	return ws
}

func TestPodExecGuardsAndValidation(t *testing.T) {
	m := seedExecModel(t)
	srv := NewServer(Options{Exec: &fakePodExec{model: m}})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	cases := []struct {
		name   string
		query  string
		origin string
		sfs    string
		want   int
	}{
		{name: "protected kube-system", query: "id=core/Pod/kube-system/coredns-0", origin: ts.URL, want: http.StatusForbidden},
		{name: "protected visual-kubernetes", query: "id=core/Pod/visual-kubernetes/vk-0", origin: ts.URL, want: http.StatusForbidden},
		{name: "protected gke-managed", query: "id=core/Pod/gke-managed-dpv2/agent-0", origin: ts.URL, want: http.StatusForbidden},
		{name: "missing Origin", query: "id=core/Pod/shop/web-0", origin: "", want: http.StatusForbidden},
		{name: "evil Origin", query: "id=core/Pod/shop/web-0", origin: "https://evil.example", want: http.StatusForbidden},
		{name: "cross-site Sec-Fetch-Site", query: "id=core/Pod/shop/web-0", origin: ts.URL, sfs: "cross-site", want: http.StatusForbidden},
		{name: "non-Pod kind", query: "id=apps/Deployment/shop/web", origin: ts.URL, want: http.StatusBadRequest},
		{name: "disallowed shell", query: "id=core/Pod/shop/web-0&shell=/bin/zsh", origin: ts.URL, want: http.StatusBadRequest},
		{name: "unknown container", query: "id=core/Pod/shop/web-0&container=ghost", origin: ts.URL, want: http.StatusBadRequest},
		{name: "missing pod", query: "id=core/Pod/shop/missing", origin: ts.URL, want: http.StatusNotFound},
		{name: "pending pod", query: "id=core/Pod/shop/pending-0", origin: ts.URL, want: http.StatusConflict},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+"/api/demo/exec?"+tc.query, nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.sfs != "" {
				req.Header.Set("Sec-Fetch-Site", tc.sfs)
			}
			resp, err := ts.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}

func TestPodExecRoundTripAndResize(t *testing.T) {
	m := seedExecModel(t)
	fe := &fakePodExec{model: m}
	srv := NewServer(Options{Exec: fe})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ws := dialTestExecWS(t, ts.URL, "id=core/Pod/shop/web-0&container=sidecar&shell=/bin/bash", ts.URL)

	var ready execServerFrame
	if err := websocket.JSON.Receive(ws, &ready); err != nil {
		t.Fatalf("receive ready: %v", err)
	}
	if ready.Type != "ready" || ready.Container != "sidecar" || ready.Shell != "/bin/bash" || len(ready.Containers) != 2 {
		t.Fatalf("unexpected ready frame: %+v", ready)
	}

	if err := websocket.JSON.Send(ws, execClientFrame{Type: "resize", Cols: 132, Rows: 42}); err != nil {
		t.Fatalf("send resize: %v", err)
	}
	if err := websocket.JSON.Send(ws, execClientFrame{Type: "stdin", Data: "hello-pod\n"}); err != nil {
		t.Fatalf("send stdin: %v", err)
	}

	var out execServerFrame
	if err := websocket.JSON.Receive(ws, &out); err != nil {
		t.Fatalf("receive stdout: %v", err)
	}
	if out.Type != "stdout" || out.Data != "out:hello-pod\n" {
		t.Fatalf("stdout frame = %+v, want out:hello-pod", out)
	}

	if err := websocket.JSON.Send(ws, execClientFrame{Type: "stdin", Data: "exit\n"}); err != nil {
		t.Fatalf("send exit: %v", err)
	}
	var exitFrame execServerFrame
	if err := websocket.JSON.Receive(ws, &exitFrame); err != nil {
		t.Fatalf("receive exit: %v", err)
	}
	if exitFrame.Type != "exit" {
		t.Fatalf("frame = %+v, want exit", exitFrame)
	}

	fe.mu.Lock()
	sizes := append([]remotecommand.TerminalSize(nil), fe.lastSizes...)
	fe.mu.Unlock()
	foundResize := false
	for _, sz := range sizes {
		if sz.Width == 132 && sz.Height == 42 {
			foundResize = true
		}
	}
	if !foundResize {
		t.Fatalf("expected resize 132x42 in %+v", sizes)
	}
}

func TestPodExecSessionCap429(t *testing.T) {
	m := seedExecModel(t)
	srv := NewServer(Options{Exec: &fakePodExec{model: m}, MaxExecSessions: 4})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	var conns []*websocket.Conn
	for i := range 4 {
		ws := dialTestExecWS(t, ts.URL, "id=core/Pod/shop/web-0", ts.URL)
		var ready execServerFrame
		if err := websocket.JSON.Receive(ws, &ready); err != nil {
			t.Fatalf("conn %d ready: %v", i, err)
		}
		conns = append(conns, ws)
	}

	// 5th session must receive HTTP 429 before upgrading.
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+"/api/demo/exec?id=core/Pod/shop/web-0", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", ts.URL)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("5th session status = %d, want 429", resp.StatusCode)
	}

	// Closing one session frees a slot for a new connection.
	_ = websocket.JSON.Send(conns[0], execClientFrame{Type: "stdin", Data: "exit\n"})
	_ = conns[0].Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/demo/exec?id=core/Pod/shop/web-0"
		cfg, _ := websocket.NewConfig(wsURL, ts.URL)
		ws, err := websocket.DialConfig(cfg)
		if err == nil {
			_ = ws.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("slot did not reopen after closing an active session")
}

func TestPodExecMissingShellDistrolessMessage(t *testing.T) {
	m := seedExecModel(t)
	fe := &fakePodExec{
		model: m,
		onExec: func(context.Context, ExecRequest) error {
			return errors.New(`OCI runtime exec failed: exec failed: unable to start container process: exec: "/bin/sh": stat /bin/sh: no such file or directory`)
		},
	}
	srv := NewServer(Options{Exec: fe})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ws := dialTestExecWS(t, ts.URL, "id=core/Pod/shop/web-0", ts.URL)
	var ready, exitFrame execServerFrame
	if err := websocket.JSON.Receive(ws, &ready); err != nil {
		t.Fatalf("ready: %v", err)
	}
	if err := websocket.JSON.Receive(ws, &exitFrame); err != nil {
		t.Fatalf("exit: %v", err)
	}
	if exitFrame.Type != "exit" || !strings.Contains(exitFrame.Message, "distroless") {
		t.Fatalf("exit frame = %+v, want clean distroless shell notice", exitFrame)
	}
}

func TestPodExecStreamStaysAlivePast15sWithActivity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		idle := newIdleWatchdog(ctx, 10*time.Minute, cancel)
		defer idle.stop()

		// Simulate 25 seconds (> 15s requestTimeout) of interactive shell activity.
		for range 5 {
			time.Sleep(5 * time.Second)
			synctest.Wait()
			if ctx.Err() != nil || idle.expired() {
				t.Fatalf("session expired early during active frames: err=%v expired=%v", ctx.Err(), idle.expired())
			}
			idle.touch()
		}

		// Leaving the session completely idle for 10m fires the idle watchdog cleanly.
		time.Sleep(10*time.Minute + time.Second)
		synctest.Wait()
		if !idle.expired() || ctx.Err() == nil {
			t.Fatalf("expected idle watchdog to expire after 10m silence: expired=%v err=%v", idle.expired(), ctx.Err())
		}
	})
}

func TestReplayExecCarriageReturnLineDiscipline(t *testing.T) {
	var out strings.Builder
	err := (&ReplayExec{}).Exec(t.Context(), ExecRequest{
		Target: PodTarget{Namespace: "shop", Pod: "web-0", Container: "web"},
		Shell:  "/bin/sh",
		Stdin:  strings.NewReader("echo cr-only\rexit\r"),
		Stdout: &out,
		Stderr: &out,
	})
	if err != nil {
		t.Fatalf("ReplayExec CR input failed: %v", err)
	}
	if !strings.Contains(out.String(), "\r\ncr-only\r\n") {
		t.Fatalf("ReplayExec output = %q, want echoed CR-terminated command output", out.String())
	}
}

func TestWSFrameWriterPreservesSplitUTF8Runes(t *testing.T) {
	var sent strings.Builder
	touches := 0
	w := &wsFrameWriter{
		send: func(f execServerFrame) error {
			sent.WriteString(f.Data)
			return nil
		},
		touch: func() { touches++ },
	}
	// Split 3-byte "─" (\xe2\x94\x80) and 4-byte "🚀" (\xf0\x9f\x9a\x80) across chunk boundaries.
	chunks := [][]byte{
		[]byte("box: \xe2\x94"),
		[]byte("\x80 \xf0"),
		[]byte("\x9f\x9a"),
		[]byte("\x80\n"),
	}
	for _, ch := range chunks {
		if n, err := w.Write(ch); err != nil || n != len(ch) {
			t.Fatalf("Write(%x) = (%d, %v), want (%d, nil)", ch, n, err, len(ch))
		}
	}
	if err := w.flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if got, want := sent.String(), "box: ─ 🚀\n"; got != want {
		t.Fatalf("reassembled UTF-8 = %q, want %q", got, want)
	}
	if touches == 0 {
		t.Fatal("expected touch() on successful frame sends")
	}
}

func TestPodExecWriteErrorCancelsSessionAndReleasesSlot(t *testing.T) {
	m := seedExecModel(t)
	writeStarted := make(chan struct{})
	execDone := make(chan error, 1)
	fe := &fakePodExec{
		model: m,
		onExec: func(ctx context.Context, req ExecRequest) error {
			close(writeStarted)
			for {
				if _, err := req.Stdout.Write([]byte("tick\n")); err != nil {
					execDone <- err
					return err
				}
				select {
				case <-ctx.Done():
					execDone <- ctx.Err()
					return ctx.Err()
				case <-time.After(10 * time.Millisecond):
				}
			}
		},
	}
	srv := NewServer(Options{Exec: fe, MaxExecSessions: 1})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ws := dialTestExecWS(t, ts.URL, "id=core/Pod/shop/web-0", ts.URL)
	var ready execServerFrame
	if err := websocket.JSON.Receive(ws, &ready); err != nil {
		t.Fatalf("ready: %v", err)
	}
	<-writeStarted
	// Drop the client connection abruptly while stdout keeps streaming; write failure must abort Exec
	// and release the sole execGate slot promptly rather than resetting idleWatchdog for 30m.
	_ = ws.Close()

	select {
	case <-execDone:
	case <-time.After(3 * time.Second):
		t.Fatal("broken WebSocket stdout write failed to abort Exec session")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ws2, err := websocket.Dial(
			"ws"+strings.TrimPrefix(ts.URL, "http")+"/api/demo/exec?id=core/Pod/shop/web-0",
			"",
			ts.URL,
		)
		if err == nil {
			_ = ws2.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("execGate slot remained leaked after WebSocket write error")
}
