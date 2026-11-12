package demo

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/net/websocket"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

const (
	defaultMaxExecSessions = 4
	defaultExecIdleTimeout = 10 * time.Minute
	defaultExecMaxLifetime = 30 * time.Minute
	maxExecFrameBytes      = 16 << 10
)

// PodTarget describes a validated running Pod and container ready for exec.
type PodTarget struct {
	Namespace  string
	Pod        string
	Container  string
	Containers []string
}

// ExecRequest carries the streams and resize queue for one interactive pod shell session.
type ExecRequest struct {
	Target    PodTarget
	Shell     string
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
	SizeQueue remotecommand.TerminalSizeQueue
}

// PodExecutor resolves running non-system pods and streams PTY sessions into them.
type PodExecutor interface {
	ResolvePod(ref model.Ref, container string) (PodTarget, *StatusErr)
	Exec(ctx context.Context, req ExecRequest) error
}

type execClientFrame struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
}

type execServerFrame struct {
	Type       string   `json:"type"`
	Data       string   `json:"data,omitempty"`
	Message    string   `json:"message,omitempty"`
	Container  string   `json:"container,omitempty"`
	Containers []string `json:"containers,omitempty"`
	Shell      string   `json:"shell,omitempty"`
}

type execGate struct {
	mu     sync.Mutex
	active int
	max    int
}

func newExecGate(maxSessions int) *execGate {
	if maxSessions <= 0 {
		maxSessions = defaultMaxExecSessions
	}
	return &execGate{max: maxSessions}
}

func (g *execGate) acquire() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active >= g.max {
		return false
	}
	g.active++
	return true
}

func (g *execGate) release() {
	g.mu.Lock()
	if g.active > 0 {
		g.active--
	}
	g.mu.Unlock()
}

func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
	if !hasSameOriginHeaders(r) {
		forbid(w, "same-origin request required")
		return
	}
	if s.exec == nil {
		writePlain(w, http.StatusServiceUnavailable, "pod exec unavailable")
		return
	}
	ref, shell, st := parseExecQuery(r)
	if st != nil {
		writePlain(w, st.Code, st.Msg)
		return
	}
	target, st := s.exec.ResolvePod(ref, strings.TrimSpace(r.URL.Query().Get("container")))
	if st != nil {
		writePlain(w, st.Code, st.Msg)
		return
	}
	if !s.execGate.acquire() {
		writePlain(w, http.StatusTooManyRequests, "too many active exec sessions (max 4)")
		return
	}
	defer s.execGate.release()

	wsSrv := websocket.Server{
		Handshake: func(*websocket.Config, *http.Request) error { return nil },
		Handler: func(ws *websocket.Conn) {
			s.serveExecSession(r.Context(), ws, target, shell)
		},
	}
	wsSrv.ServeHTTP(w, r)
}

func parseExecQuery(r *http.Request) (model.Ref, string, *StatusErr) {
	ref, err := model.ParseID(strings.TrimSpace(r.URL.Query().Get("id")))
	if err != nil {
		return model.Ref{}, "", &StatusErr{Code: http.StatusBadRequest, Msg: "malformed pod id"}
	}
	if ref.Group != "core" || ref.Kind != "Pod" {
		return model.Ref{}, "", &StatusErr{Code: http.StatusBadRequest, Msg: "unsupported resource kind for exec"}
	}
	if IsProtectedNamespace(ref.Namespace) {
		return model.Ref{}, "", &StatusErr{Code: http.StatusForbidden, Msg: "protected namespace is refused in demo mode"}
	}
	shell := strings.TrimSpace(r.URL.Query().Get("shell"))
	if shell == "" {
		shell = "/bin/sh"
	}
	if shell != "/bin/sh" && shell != "/bin/bash" {
		return model.Ref{}, "", &StatusErr{Code: http.StatusBadRequest, Msg: "shell must be /bin/sh or /bin/bash"}
	}
	return ref, shell, nil
}

func (s *Server) serveExecSession(parent context.Context, ws *websocket.Conn, target PodTarget, shell string) {
	ws.MaxPayloadBytes = maxExecFrameBytes
	ctx, cancel := context.WithTimeout(parent, s.execMaxLifetime)
	defer cancel()

	var writeMu sync.Mutex
	send := func(f execServerFrame) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		if err := ws.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
			return err
		}
		return websocket.JSON.Send(ws, f)
	}

	idle := newIdleWatchdog(ctx, s.execIdleTimeout, cancel)
	defer idle.stop()

	if err := send(execServerFrame{Type: "ready", Container: target.Container, Containers: target.Containers, Shell: shell}); err != nil {
		_ = ws.Close()
		return
	}

	stdinR, stdinW := io.Pipe()
	sizes := newTermSizeQueue(ctx.Done())
	go readExecFrames(ctx, ws, stdinW, sizes, idle.touch, cancel)

	out := &wsFrameWriter{send: send, touch: idle.touch, onErr: func() {
		cancel()
		_ = ws.Close()
	}}
	err := s.exec.Exec(ctx, ExecRequest{
		Target: target, Shell: shell, Stdin: stdinR, Stdout: out, Stderr: out, SizeQueue: sizes,
	})
	_ = out.flush()
	_ = stdinR.Close() // unblock reader goroutine on stream exit
	sizes.close()

	_ = send(execServerFrame{Type: "exit", Message: formatExecExit(err, idle.expired())}) // best-effort exit notice before closing
	_ = ws.Close()
}

func readExecFrames(
	ctx context.Context,
	ws *websocket.Conn,
	stdinW *io.PipeWriter,
	sizes *termSizeQueue,
	touch func(),
	cancel context.CancelFunc,
) {
	defer func() {
		_ = stdinW.Close()
		cancel()
	}()
	for {
		var f execClientFrame
		if err := websocket.JSON.Receive(ws, &f); err != nil {
			return
		}
		touch()
		switch f.Type {
		case "stdin":
			if f.Data != "" {
				if _, err := io.WriteString(stdinW, f.Data); err != nil {
					return
				}
			}
		case "resize":
			if f.Cols > 0 && f.Rows > 0 && f.Cols <= 500 && f.Rows <= 200 {
				sizes.push(remotecommand.TerminalSize{Width: f.Cols, Height: f.Rows})
			}
		}
		if ctx.Err() != nil {
			return
		}
	}
}

func formatExecExit(err error, idleExpired bool) string {
	if idleExpired {
		return "Session closed after idle timeout."
	}
	if err == nil || errors.Is(err, context.Canceled) {
		return "Shell session ended."
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "Session reached maximum 30m demo duration."
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "executable file not found") ||
		strings.Contains(msg, "no such file or directory") ||
		strings.Contains(msg, "unable to start container process") ||
		strings.Contains(msg, "oci runtime exec failed") {
		return "Container has no shell (/bin/sh or /bin/bash unavailable — for example a distroless image)."
	}
	return "Shell exited with an error."
}

type wsFrameWriter struct {
	mu    sync.Mutex
	tail  []byte
	send  func(execServerFrame) error
	touch func()
	onErr func()
}

func (w *wsFrameWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	w.mu.Lock()
	buf := p
	if len(w.tail) > 0 {
		buf = append(append([]byte(nil), w.tail...), p...)
		w.tail = nil
	}
	emit, rem := splitTrailingIncompleteUTF8(buf)
	if len(rem) > 0 {
		w.tail = append(w.tail[:0], rem...)
	}
	w.mu.Unlock()

	if len(emit) > 0 {
		if err := w.send(execServerFrame{Type: "stdout", Data: string(emit)}); err != nil {
			if w.onErr != nil {
				w.onErr()
			}
			return 0, err
		}
		if w.touch != nil {
			w.touch()
		}
	}
	return len(p), nil
}

func (w *wsFrameWriter) flush() error {
	w.mu.Lock()
	if len(w.tail) == 0 {
		w.mu.Unlock()
		return nil
	}
	rem := string(w.tail)
	w.tail = nil
	w.mu.Unlock()
	return w.send(execServerFrame{Type: "stdout", Data: rem})
}

func splitTrailingIncompleteUTF8(b []byte) (emit, tail []byte) {
	n := len(b)
	start := n - 1
	for start > 0 && n-start < utf8.UTFMax && !utf8.RuneStart(b[start]) {
		start--
	}
	if !utf8.FullRune(b[start:]) && b[start] >= 0xC2 && b[start] <= 0xF4 {
		return b[:start], b[start:]
	}
	return b, nil
}

type termSizeQueue struct {
	ch   chan remotecommand.TerminalSize
	done <-chan struct{}
	once sync.Once
}

func newTermSizeQueue(done <-chan struct{}) *termSizeQueue {
	q := &termSizeQueue{ch: make(chan remotecommand.TerminalSize, 4), done: done}
	q.ch <- remotecommand.TerminalSize{Width: 80, Height: 24}
	return q
}

func (q *termSizeQueue) push(sz remotecommand.TerminalSize) {
	select {
	case q.ch <- sz:
	default:
	}
}

func (q *termSizeQueue) close() {
	q.once.Do(func() {
		select {
		case q.ch <- remotecommand.TerminalSize{}:
		default:
		}
	})
}

func (q *termSizeQueue) Next() *remotecommand.TerminalSize {
	select {
	case <-q.done:
		return nil
	case sz := <-q.ch:
		if sz.Width == 0 && sz.Height == 0 {
			return nil
		}
		return &sz
	}
}

type idleWatchdog struct {
	mu      sync.Mutex
	timer   *time.Timer
	timeout time.Duration
	fired   bool
}

func newIdleWatchdog(ctx context.Context, timeout time.Duration, cancel context.CancelFunc) *idleWatchdog {
	w := &idleWatchdog{timeout: timeout}
	w.timer = time.AfterFunc(timeout, func() {
		if ctx.Err() != nil {
			return
		}
		w.mu.Lock()
		w.fired = true
		w.mu.Unlock()
		cancel()
	})
	return w
}

func (w *idleWatchdog) touch() {
	w.mu.Lock()
	if !w.fired {
		w.timer.Reset(w.timeout)
	}
	w.mu.Unlock()
}

func (w *idleWatchdog) stop() {
	w.timer.Stop()
}

func (w *idleWatchdog) expired() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.fired
}
