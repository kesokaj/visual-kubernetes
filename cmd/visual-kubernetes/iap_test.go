package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"
	"google.golang.org/api/idtoken"

	"github.com/kesokaj/visual-kubernetes/internal/iapauth"
)

type stubIAPValidator struct {
	tokens map[string]*idtoken.Payload
}

func (s stubIAPValidator) Validate(_ context.Context, token, audience string) (*idtoken.Payload, error) {
	p, ok := s.tokens[token]
	if !ok || p == nil {
		return nil, errors.New("invalid or expired token")
	}
	if p.Audience != audience {
		return nil, fmt.Errorf("audience mismatch: got %q, want %q", p.Audience, audience)
	}
	return p, nil
}

func TestRunIAPAndAllowedHostStartupValidation(t *testing.T) {
	fixture := filepath.Join("..", "..", "test", "fixtures", "streams", "deploy-scale.ndjson")
	cases := []struct {
		name string
		args []string
	}{
		{"empty explicit iap-audience", []string{"--replay", fixture, "--iap-audience=", "--allowed-host=vk.example.internal"}},
		{"whitespace iap-audience", []string{"--replay", fixture, "--iap-audience=   ", "--allowed-host=vk.example.internal"}},
		{"bad iap-audience prefix", []string{"--replay", fixture, "--iap-audience=https://example.com", "--allowed-host=vk.example.internal"}},
		{"missing project number", []string{"--replay", fixture, "--iap-audience=/projects/abc/global/backendServices/1", "--allowed-host=vk.example.internal"}},
		{"missing allowed-host with iap-audience", []string{"--replay", fixture, "--iap-audience=/projects/123456/global/backendServices/789"}},
		{"empty explicit allowed-host", []string{"--replay", fixture, "--allowed-host="}},
		{"wildcard allowed-host", []string{"--replay", fixture, "--allowed-host=*.example.internal"}},
		{"scheme in allowed-host", []string{"--replay", fixture, "--allowed-host=https://vk.example.internal"}},
		{"port in allowed-host", []string{"--replay", fixture, "--allowed-host=vk.example.internal:8443"}},
		{"ip literal in allowed-host", []string{"--replay", fixture, "--allowed-host=10.0.0.1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			code := run(t.Context(), tc.args, io.Discard, &stderr, discard())
			if code != 1 {
				t.Errorf("run(%v) = %d, want 1", tc.args, code)
			}
		})
	}
}

func TestRunServeIAPGatesAllRoutesAndUnlocksDemoOnAllowedHost(t *testing.T) {
	const (
		goodAud = "/projects/123456789012/global/backendServices/9876543210"
		allowed = "vk.example.internal"
	)
	validator := stubIAPValidator{
		tokens: map[string]*idtoken.Payload{
			"valid-jwt": {
				Issuer:   iapauth.Issuer,
				Audience: goodAud,
				Subject:  "accounts.google.com:112233",
				Claims:   map[string]any{"email": "presenter@example.com"},
			},
			"wrong-aud-jwt": {
				Issuer:   iapauth.Issuer,
				Audience: "/projects/999999999999/global/backendServices/1111111111",
				Subject:  "accounts.google.com:112233",
			},
			"wrong-iss-jwt": {
				Issuer:   "https://accounts.google.com",
				Audience: goodAud,
				Subject:  "accounts.google.com:112233",
			},
		},
	}

	fixture := filepath.Join("..", "..", "test", "fixtures", "streams", "deploy-scale.ndjson")
	var logs lockedBuffer
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- runServe(ctx, serveOptions{
			addr:           "127.0.0.1:0",
			replay:         fixture,
			logs:           true,
			demo:           true,
			allowedHost:    allowed,
			allowedHostSet: true,
			iapAudience:    goodAud,
			iapAudienceSet: true,
			iapValidator:   validator,
		}, slog.New(slog.NewJSONHandler(&logs, nil)))
	}()
	t.Cleanup(func() { cancel(); <-done })

	var addr string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if _, rest, ok := strings.Cut(logs.String(), `"msg":"serving","addr":"`); ok {
			addr, _, _ = strings.Cut(rest, `"`)
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if addr == "" {
		t.Fatalf("server never logged address: %s", logs.String())
	}

	cases := []struct {
		name    string
		method  string
		host    string
		path    string
		headers map[string]string
		body    string
		want    int
	}{
		{
			name:   "pod-IP GET /healthz exempted for kubelet and GLB",
			method: http.MethodGet,
			host:   "10.244.0.9:8080",
			path:   "/healthz",
			want:   http.StatusOK,
		},
		{
			name:   "pod-IP non-healthz route requires IAP",
			method: http.MethodGet,
			host:   "10.244.0.9:8080",
			path:   "/api/demo/ports",
			want:   http.StatusUnauthorized,
		},
		{
			name:   "DNS Host GET /healthz requires IAP",
			method: http.MethodGet,
			host:   allowed,
			path:   "/healthz",
			want:   http.StatusUnauthorized,
		},
		{
			name:   "DNS Host UI root without JWT returns 401",
			method: http.MethodGet,
			host:   allowed,
			path:   "/",
			want:   http.StatusUnauthorized,
		},
		{
			name:    "spoofed X-Goog-Authenticated-User-Email without JWT returns 401",
			method:  http.MethodGet,
			host:    allowed,
			path:    "/api/demo/ports",
			headers: map[string]string{"X-Goog-Authenticated-User-Email": "accounts.google.com:admin@example.com"},
			want:    http.StatusUnauthorized,
		},
		{
			name:    "expired JWT returns 401",
			method:  http.MethodGet,
			host:    allowed,
			path:    "/api/demo/ports",
			headers: map[string]string{iapauth.AssertionHeader: "expired-jwt"},
			want:    http.StatusUnauthorized,
		},
		{
			name:    "wrong audience JWT returns 401",
			method:  http.MethodGet,
			host:    allowed,
			path:    "/api/demo/ports",
			headers: map[string]string{iapauth.AssertionHeader: "wrong-aud-jwt"},
			want:    http.StatusUnauthorized,
		},
		{
			name:    "wrong issuer JWT returns 401",
			method:  http.MethodGet,
			host:    allowed,
			path:    "/api/demo/ports",
			headers: map[string]string{iapauth.AssertionHeader: "wrong-iss-jwt"},
			want:    http.StatusUnauthorized,
		},
		{
			name:    "unlisted DNS Host rejected by HostGuard with 403 even with valid JWT",
			method:  http.MethodGet,
			host:    "evil.example.internal",
			path:    "/api/demo/ports",
			headers: map[string]string{iapauth.AssertionHeader: "valid-jwt"},
			want:    http.StatusForbidden,
		},
		{
			name:    "valid IAP JWT on allowed-host serves UI root",
			method:  http.MethodGet,
			host:    allowed,
			path:    "/",
			headers: map[string]string{iapauth.AssertionHeader: "valid-jwt"},
			want:    http.StatusOK,
		},
		{
			name:    "valid IAP JWT on allowed-host unlocks --demo GET /api/demo/ports",
			method:  http.MethodGet,
			host:    allowed,
			path:    "/api/demo/ports",
			headers: map[string]string{iapauth.AssertionHeader: "valid-jwt"},
			want:    http.StatusOK,
		},
		{
			name:   "valid IAP JWT on allowed-host allows HTTPS SameOrigin PATCH /api/demo/workloads/env",
			method: http.MethodPatch,
			host:   allowed,
			path:   "/api/demo/workloads/env",
			headers: map[string]string{
				iapauth.AssertionHeader: "valid-jwt",
				"Origin":                "https://" + allowed,
				"Content-Type":          "application/json",
			},
			body: `{"id":"apps/Deployment/shop/web","resourceVersion":"1","container":"web","env":[{"name":"LOG_LEVEL","value":"info"}]}`,
			want: http.StatusOK,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var bodyReader io.Reader
			if tc.body != "" {
				bodyReader = strings.NewReader(tc.body)
			}
			req, err := http.NewRequestWithContext(t.Context(), tc.method, "http://"+addr+tc.path, bodyReader)
			if err != nil {
				t.Fatal(err)
			}
			req.Host = tc.host
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Errorf("%s %s (Host=%s) = %d, want %d", tc.method, tc.path, tc.host, resp.StatusCode, tc.want)
			}
		})
	}
}

func TestNewClientsExecCfgIsUntimed(t *testing.T) {
	kcPath := filepath.Join(t.TempDir(), "kubeconfig")
	const raw = `apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: https://127.0.0.1:6443
contexts:
- name: test
  context:
    cluster: test
    user: test
current-context: test
users:
- name: test
  user:
    token: fake-token
`
	if err := os.WriteFile(kcPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cl, err := newClients(kcPath, discard())
	if err != nil {
		t.Fatal(err)
	}
	if cl.cfg.Timeout != requestTimeout {
		t.Errorf("cfg.Timeout = %v, want %v", cl.cfg.Timeout, requestTimeout)
	}
	if cl.execCfg == nil || cl.execCfg.Timeout != 0 {
		t.Errorf("execCfg = %+v, want non-nil with Timeout 0", cl.execCfg)
	}
}

func TestRunDemoExecEndpointWired(t *testing.T) {
	offAddr := startTestRun(t)
	offResp, err := http.Get("http://" + offAddr + "/api/demo/exec?id=core/Pod/shop/web-1&shell=/bin/sh")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, offResp.Body)
	_ = offResp.Body.Close()
	if offResp.StatusCode != http.StatusForbidden {
		t.Fatalf("--demo=false GET /api/demo/exec = %d, want 403", offResp.StatusCode)
	}

	addr := startTestRun(t, "--demo")
	// Wait briefly for replay stream to populate core/Pod/shop/web-* in Hub.
	_ = readSnapshotFromStream(t, "http://"+addr)
	snap := readSnapshotFromStream(t, "http://"+addr)
	var runningPodID string
	for _, e := range snap.Entities {
		if e.Kind == "Pod" && e.Namespace == "shop" && e.Status == "Running" {
			runningPodID = e.ID
			break
		}
	}
	if runningPodID == "" {
		t.Skip("no running shop pod in initial snapshot")
	}

	wsCfg, err := websocket.NewConfig("ws://"+addr+"/api/demo/exec?id="+runningPodID+"&shell=/bin/sh", "http://"+addr)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := websocket.DialConfig(wsCfg)
	if err != nil {
		t.Fatalf("DialConfig(/api/demo/exec): %v", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := websocket.Message.Send(conn, `{"type":"stdin","data":"echo wired-ok\nexit\n"}`); err != nil {
		t.Fatal(err)
	}
	var combined strings.Builder
	for range 64 {
		var msg string
		if err := websocket.Message.Receive(conn, &msg); err != nil {
			break
		}
		combined.WriteString(msg)
		if strings.Contains(msg, `"type":"exit"`) {
			break
		}
	}
	if !strings.Contains(combined.String(), "wired-ok") {
		t.Errorf("exec output = %q, want wired-ok", combined.String())
	}
}
