package demo

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	"github.com/kesokaj/visual-kubernetes/internal/stream"
)

// ReplayExec implements PodExecutor against a replay Hub's entities so offline Playwright tests
// exercise the full /api/demo/exec WebSocket PTY contract without a cluster.
type ReplayExec struct {
	Hub *stream.Hub
}

// ResolvePod checks that ref names a Running non-protected Pod in the replay Hub.
func (r *ReplayExec) ResolvePod(ref model.Ref, container string) (PodTarget, *StatusErr) {
	if r == nil || r.Hub == nil {
		return PodTarget{}, &StatusErr{Code: http.StatusServiceUnavailable, Msg: "replay hub unavailable"}
	}
	if IsProtectedNamespace(ref.Namespace) {
		return PodTarget{}, &StatusErr{Code: http.StatusForbidden, Msg: "protected namespace is refused in demo mode"}
	}
	ents, _ := r.Hub.Snapshot()
	var found *model.Entity
	for i := range ents {
		if ents[i].ID == ref.ID() && !ents[i].Folded {
			found = &ents[i]
			break
		}
	}
	if found == nil {
		return PodTarget{}, &StatusErr{Code: http.StatusNotFound, Msg: "pod not found"}
	}
	if found.Status != "Running" {
		return PodTarget{}, &StatusErr{Code: http.StatusConflict, Msg: "pod is not running"}
	}
	base := strings.SplitN(found.Name, "-", 2)[0]
	if base == "" {
		base = "app"
	}
	containers := []string{base}
	if base == "worker" {
		containers = append(containers, "distroless-helper")
	}
	chosen := container
	if chosen == "" {
		chosen = containers[0]
	}
	valid := false
	for _, c := range containers {
		if c == chosen {
			valid = true
			break
		}
	}
	if !valid {
		return PodTarget{}, &StatusErr{Code: http.StatusBadRequest, Msg: "unknown container"}
	}
	return PodTarget{Namespace: found.Namespace, Pod: found.Name, Container: chosen, Containers: containers}, nil
}

// Exec runs a deterministic interactive shell simulation for --replay --demo.
func (r *ReplayExec) Exec(ctx context.Context, req ExecRequest) error {
	if req.Target.Container == "distroless-helper" {
		return fmt.Errorf("unable to start container process: exec: %q: executable file not found in $PATH", req.Shell)
	}
	// Drain resize events in the background so SizeQueue.Next() never blocks callers.
	if req.SizeQueue != nil {
		go func() {
			for req.SizeQueue.Next() != nil {
			}
		}()
	}
	prompt := fmt.Sprintf("%s@%s:/$ ", req.Target.Container, req.Target.Pod)
	_, _ = io.WriteString(req.Stdout, fmt.Sprintf("Connected to %s/%s (%s)\r\n%s", req.Target.Namespace, req.Target.Pod, req.Shell, prompt))

	done := make(chan error, 1)
	go func() {
		done <- readReplayPTY(req, prompt)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		return err
	}
}

// readReplayPTY emulates a PTY line discipline (ICRNL + ECHO) so both '\r' (xterm.js Enter)
// and '\n' submit lines cleanly without hanging on carriage-return-only terminal input.
func readReplayPTY(req ExecRequest, prompt string) error {
	var buf [1024]byte
	var line strings.Builder
	var echo strings.Builder
	lastCR := false
	flushEcho := func() {
		if echo.Len() > 0 {
			_, _ = io.WriteString(req.Stdout, echo.String())
			echo.Reset()
		}
	}
	for {
		n, err := req.Stdin.Read(buf[:])
		for i := range n {
			b := buf[i]
			if b == '\n' && lastCR {
				lastCR = false
				continue
			}
			lastCR = b == '\r'
			if b == '\r' || b == '\n' {
				flushEcho()
				cmd := strings.TrimSpace(line.String())
				line.Reset()
				if !handleReplayLine(req, prompt, cmd) {
					return nil
				}
				continue
			}
			if b == 0x7f || b == '\b' {
				s := line.String()
				if len(s) > 0 {
					line.Reset()
					line.WriteString(s[:len(s)-1])
					echo.WriteString("\b \b")
				}
				continue
			}
			line.WriteByte(b)
			echo.WriteByte(b)
		}
		flushEcho()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func handleReplayLine(req ExecRequest, prompt, line string) bool {
	switch {
	case line == "":
		_, _ = io.WriteString(req.Stdout, "\r\n"+prompt)
	case line == "exit":
		_, _ = io.WriteString(req.Stdout, "\r\nlogout\r\n")
		return false
	case line == "whoami":
		_, _ = io.WriteString(req.Stdout, "\r\ndemo-user\r\n"+prompt)
	case line == "pwd":
		_, _ = io.WriteString(req.Stdout, "\r\n/app\r\n"+prompt)
	case strings.HasPrefix(line, "echo "):
		_, _ = io.WriteString(req.Stdout, "\r\n"+strings.TrimPrefix(line, "echo ")+"\r\n"+prompt)
	default:
		_, _ = io.WriteString(req.Stdout, fmt.Sprintf("\r\nreplay-sh: executed %s\r\n%s", line, prompt))
	}
	return true
}
