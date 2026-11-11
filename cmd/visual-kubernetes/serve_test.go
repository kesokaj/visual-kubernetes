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

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8swatch "k8s.io/apimachinery/pkg/watch"
	fakediscovery "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kesokaj/visual-kubernetes/internal/hubble"
	"github.com/kesokaj/visual-kubernetes/internal/model"
	"github.com/kesokaj/visual-kubernetes/internal/replay"
	"github.com/kesokaj/visual-kubernetes/internal/stream"
	"github.com/kesokaj/visual-kubernetes/internal/watch"
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
	a, err := startReplay(t.Context(), filepath.Join("..", "..", "test", "fixtures", "streams", "deploy-scale.ndjson"), true, discard())
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
	if _, err := startReplay(t.Context(), filepath.Join(t.TempDir(), "absent.ndjson"), true, discard()); err == nil {
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
		done <- runServe(ctx, serveOptions{addr: "127.0.0.1:0", logs: true,
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

// TestRunLogsFlag drives run() with default flags (logs enabled) and with --logs=false (403).
func TestRunLogsFlag(t *testing.T) {
	fixture := filepath.Join("..", "..", "test", "fixtures", "streams", "deploy-scale.ndjson")
	podURL := "/api/logs?id=core/Pod/shop/web-7d4b9c-x001"
	for _, tc := range []struct {
		name     string
		extra    []string
		wantCode int
		wantSub  string
	}{
		{"default logs on", nil, http.StatusOK, "replay mode"},
		{"logs=false", []string{"--logs=false"}, http.StatusForbidden, "logs are off on this install"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs lockedBuffer
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan int, 1)
			args := append([]string{"--addr", "127.0.0.1:0", "--replay", fixture}, tc.extra...)
			go func() {
				done <- run(ctx, args, io.Discard, io.Discard, slog.New(slog.NewJSONHandler(&logs, nil)))
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
			resp, err := http.Get("http://" + addr + podURL)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != tc.wantCode || !strings.Contains(string(body), tc.wantSub) {
				t.Errorf("GET %s = %d %q, want %d containing %q", podURL, resp.StatusCode, string(body), tc.wantCode, tc.wantSub)
			}
		})
	}
}

// TestNewLiveServerLogsFlag checks the live-cluster Server wiring: --logs=false leaves Logs nil
// and answers 403 before any API call.
func TestNewLiveServerLogsFlag(t *testing.T) {
	cs := fake.NewSimpleClientset()
	srv := newLiveServer(stream.NewHub(stream.HubOptions{}), func() bool { return true }, model.New(), clients{cs: cs}, false, discard())
	if !srv.LogsDisabled || srv.Logs != nil {
		t.Fatalf("live server with logs=false: LogsDisabled=%v Logs=%v, want true, nil", srv.LogsDisabled, srv.Logs)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/logs?id=core/Pod/ns/a", nil))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "logs are off on this install") {
		t.Errorf("live GET /api/logs = %d %q, want 403 logs-off", rec.Code, rec.Body.String())
	}
	if acts := cs.Actions(); len(acts) != 0 {
		t.Errorf("live logs=false made %d API calls, want 0", len(acts))
	}
}

// TestChainSinkPrimesThenObserves verifies that initial-list items prime the causal-chain
// builder silently (even inside a mixed batch), while live watch changes publish `chain`
// events immediately with no post-sync polling gap.
func TestChainSinkPrimesThenObserves(t *testing.T) {
	hub := stream.NewHub(stream.HubOptions{})
	m := model.New()
	if err := hub.Publish(m.SnapshotEvent(map[string]model.Capability{})); err != nil {
		t.Fatal(err)
	}
	sub, err := hub.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	t0 := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	sink := newChainSink(m, replay.New(), func() time.Time { return t0 }, hub, discard())

	r2 := int32(2)
	existing := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "shop", ResourceVersion: "1"},
		Spec:       appsv1.DeploymentSpec{Replicas: &r2},
	}
	sink([]model.Change{{Obj: existing, InitialList: true}})
	drainChains := func() []string {
		var out []string
		for {
			select {
			case ev := <-sub.C:
				if ev.Type == replay.EventChain {
					out = append(out, string(ev.Data))
				}
			default:
				return out
			}
		}
	}
	if got := drainChains(); len(got) != 0 {
		t.Fatalf("initial-list batch emitted chain events: %v", got)
	}

	// Status-only update on the primed Deployment emits no chain.
	statusOnly := existing.DeepCopy()
	statusOnly.ResourceVersion = "2"
	sink([]model.Change{{Obj: statusOnly}})
	if got := drainChains(); len(got) != 0 {
		t.Fatalf("unchanged spec on primed Deployment emitted chain events: %v", got)
	}

	// Mixed batch: another informer's initial-list Deployment arrives alongside a live scale on `web`.
	otherInit := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "shop", ResourceVersion: "1"},
		Spec:       appsv1.DeploymentSpec{Replicas: &r2},
	}
	r5 := int32(5)
	scaled := existing.DeepCopy()
	scaled.ResourceVersion = "3"
	scaled.Spec.Replicas = &r5
	t0 = t0.Add(time.Second)
	sink([]model.Change{{Obj: otherInit, InitialList: true}, {Obj: scaled, InitialList: false}})
	chains := drainChains()
	if len(chains) != 1 || !strings.Contains(chains[0], `"verb":"updated"`) || !strings.Contains(chains[0], `"apps/Deployment/shop/web"`) || strings.Contains(chains[0], `"apps/Deployment/shop/api"`) {
		t.Fatalf("mixed batch chains = %v, want only 1 updated chain for apps/Deployment/shop/web", chains)
	}

	// End-to-end through watch.Watcher: scaling immediately after watch registration (0 ms gap) emits the chain.
	t.Run("watcher-immediate-post-list-update", func(t *testing.T) {
		wHub := stream.NewHub(stream.HubOptions{})
		wm := model.New()
		if err := wHub.Publish(wm.SnapshotEvent(map[string]model.Capability{})); err != nil {
			t.Fatal(err)
		}
		wSub, err := wHub.Subscribe("")
		if err != nil {
			t.Fatal(err)
		}
		defer wSub.Close()

		deployGVR := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
		disc := &fakediscovery.FakeDiscovery{Fake: &k8stesting.Fake{Resources: []*metav1.APIResourceList{{
			GroupVersion: "apps/v1",
			APIResources: []metav1.APIResource{{Name: "deployments", Kind: "Deployment", Namespaced: true, Verbs: metav1.Verbs{"get", "list", "watch"}}},
		}}}}
		dyn := dynamicfake.NewSimpleDynamicClient(scheme.Scheme, existing.DeepCopy())
		watching := make(chan struct{})
		var once sync.Once
		dyn.PrependWatchReactor("deployments", func(a k8stesting.Action) (bool, k8swatch.Interface, error) {
			wi, werr := dyn.Tracker().Watch(a.GetResource(), a.GetNamespace())
			once.Do(func() { close(watching) })
			return true, wi, werr
		})
		w := watch.New(disc, dyn, newChainSink(wm, replay.New(), time.Now, wHub, discard()), discard())
		ctx, cancel := context.WithCancel(t.Context())
		defer func() { cancel(); w.Wait() }()
		if err := w.Start(ctx); err != nil {
			t.Fatal(err)
		}
		<-watching
		uMap, err := runtime.DefaultUnstructuredConverter.ToUnstructured(scaled)
		if err != nil {
			t.Fatal(err)
		}
		u := &unstructured.Unstructured{Object: uMap}
		u.SetAPIVersion("apps/v1")
		u.SetKind("Deployment")
		if _, err := dyn.Resource(deployGVR).Namespace("shop").Update(ctx, u, metav1.UpdateOptions{}); err != nil {
			t.Fatal(err)
		}
		deadline := time.After(5 * time.Second)
		for {
			select {
			case ev := <-wSub.C:
				if ev.Type == replay.EventChain {
					body := string(ev.Data)
					if !strings.Contains(body, `"verb":"updated"`) {
						t.Fatalf("unexpected chain verb (initial list leaked?): %s", body)
					}
					return
				}
			case <-deadline:
				t.Fatal("timed out waiting for immediate post-list update chain")
			}
		}
	})
}
