package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

func startTestRun(t *testing.T, extraArgs ...string) string {
	t.Helper()
	fixture := filepath.Join("..", "..", "test", "fixtures", "streams", "deploy-scale.ndjson")
	var logs lockedBuffer
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan int, 1)
	args := append([]string{"--addr", "127.0.0.1:0", "--replay", fixture}, extraArgs...)
	go func() {
		done <- run(ctx, args, io.Discard, io.Discard, slog.New(slog.NewJSONHandler(&logs, nil)))
	}()
	t.Cleanup(func() { cancel(); <-done })
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if _, rest, ok := strings.Cut(logs.String(), `"msg":"serving","addr":"`); ok {
			addr, _, _ := strings.Cut(rest, `"`)
			return addr
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server never logged its address: %s", logs.String())
	return ""
}

func readSnapshotFromStream(t *testing.T, baseURL string) model.SnapshotData {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	r := bufio.NewReader(resp.Body)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("reading stream snapshot: %v", err)
		}
		if after, ok := strings.CutPrefix(line, "data: "); ok {
			var snap model.SnapshotData
			if err := json.Unmarshal([]byte(after), &snap); err != nil {
				t.Fatal(err)
			}
			return snap
		}
	}
}

func TestRunDemoFlagDefaultOff(t *testing.T) {
	var stderr bytes.Buffer
	code := run(t.Context(), []string{"--help"}, io.Discard, &stderr, discard())
	if code != 2 || !strings.Contains(stderr.String(), "--demo") || !strings.Contains(strings.ToLower(stderr.String()), "non-production") {
		t.Errorf("--help = %d %q, want --demo with non-production notice", code, stderr.String())
	}

	addr := startTestRun(t)
	resp, err := http.Get("http://" + addr + "/api/demo/ports")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "demo mode is off on this install") {
		t.Errorf("default GET /api/demo/ports = %d %q, want 403 demo-off", resp.StatusCode, string(body))
	}

	snap := readSnapshotFromStream(t, "http://"+addr)
	if snap.Capabilities["demo"].State != "disabled" {
		t.Errorf("default stream demo capability = %+v, want disabled", snap.Capabilities["demo"])
	}
}

func TestRunDemoFlagEnabledAndLoopbackGuard(t *testing.T) {
	addr := startTestRun(t, "--demo")
	resp, err := http.Get("http://" + addr + "/api/demo/ports")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"ports"`) {
		t.Fatalf("--demo GET /api/demo/ports = %d %q, want 200 ports JSON", resp.StatusCode, string(body))
	}

	snap := readSnapshotFromStream(t, "http://"+addr)
	if snap.Capabilities["demo"].State != "ok" {
		t.Errorf("--demo stream capability = %+v, want ok", snap.Capabilities["demo"])
	}

	// Pod-IP Host on GET /healthz stays 200, while non-/healthz routes get 403 without IAP.
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/healthz", http.StatusOK},
		{"/", http.StatusForbidden},
		{"/api/demo/ports", http.StatusForbidden},
		{"/api/demo/workloads/env?id=apps/Deployment/shop/web", http.StatusForbidden},
	} {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+tc.path, nil)
		req.Host = "10.244.0.5:8080"
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		if r.StatusCode != tc.want {
			t.Errorf("Host 10.244.0.5:8080 GET %s = %d, want %d", tc.path, r.StatusCode, tc.want)
		}
	}
}

func TestRunDemoWorkloadEnvEndpoint(t *testing.T) {
	offAddr := startTestRun(t)
	offResp, err := http.Get("http://" + offAddr + "/api/demo/workloads/env?id=apps/Deployment/shop/web")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, offResp.Body)
	_ = offResp.Body.Close()
	if offResp.StatusCode != http.StatusForbidden {
		t.Fatalf("--demo=false GET /api/demo/workloads/env = %d, want 403", offResp.StatusCode)
	}

	addr := startTestRun(t, "--demo")
	getResp, err := http.Get("http://" + addr + "/api/demo/workloads/env?id=apps/Deployment/shop/web")
	if err != nil {
		t.Fatal(err)
	}
	getBody, _ := io.ReadAll(getResp.Body)
	_ = getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK || !strings.Contains(string(getBody), `"resourceVersion":"1"`) {
		t.Fatalf("--demo GET /api/demo/workloads/env = %d %q, want 200 rv=1", getResp.StatusCode, string(getBody))
	}

	patchBody := `{"id":"apps/Deployment/shop/web","resourceVersion":"1","container":"web","env":[{"name":"LOG_LEVEL","value":"debug"}]}`
	patchReq, _ := http.NewRequestWithContext(t.Context(), http.MethodPatch, "http://"+addr+"/api/demo/workloads/env", strings.NewReader(patchBody))
	patchReq.Header.Set("Origin", "http://"+addr)
	patchReq.Header.Set("Content-Type", "application/json")
	patchResp, err := http.DefaultClient.Do(patchReq)
	if err != nil {
		t.Fatal(err)
	}
	patchedBytes, _ := io.ReadAll(patchResp.Body)
	_ = patchResp.Body.Close()
	if patchResp.StatusCode != http.StatusOK || !strings.Contains(string(patchedBytes), `"resourceVersion":"2"`) {
		t.Fatalf("--demo PATCH /api/demo/workloads/env = %d %q, want 200 rv=2", patchResp.StatusCode, string(patchedBytes))
	}
}
