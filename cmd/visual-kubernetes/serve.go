package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	"github.com/kesokaj/visual-kubernetes/internal/probe"
	"github.com/kesokaj/visual-kubernetes/internal/sources"
	"github.com/kesokaj/visual-kubernetes/internal/stream"
	"github.com/kesokaj/visual-kubernetes/internal/watch"
)

// Listen addresses (D26). Outside a cluster the server holds the operator's credentials, so it
// listens on loopback unless --addr says otherwise; in the cluster it listens on every
// interface behind its ClusterIP Service and port-forward (R7).
const (
	localAddr     = "127.0.0.1:8080"
	inClusterAddr = ":8080"
)

// requestTimeout bounds every API request except the watches, which are long-lived by design.
const requestTimeout = 15 * time.Second

// serveOptions are the flags of the default command.
type serveOptions struct {
	kubeconfig string
	addr       string
	replay     string
}

// app is a built server: its handler, whether it runs in the cluster (for the default
// address), and the background work started on its context.
type app struct {
	handler   http.Handler
	inCluster bool
}

// runServe builds the live or replay app, listens and serves until ctx ends.
func runServe(ctx context.Context, o serveOptions, log *slog.Logger) error {
	var a app
	var err error
	if o.replay != "" {
		a, err = startReplay(ctx, o.replay, log)
	} else {
		a, err = startLive(ctx, o.kubeconfig, log)
	}
	if err != nil {
		return err
	}
	addr := listenAddr(o.addr, a.inCluster)
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", addr, err)
	}
	page := distFS()
	log.Info("serving", "addr", ln.Addr().String(), "replay", o.replay != "", "page", page != nil)
	return serveHTTP(ctx, ln, stream.HostGuard(withPage(a.handler, page), o.addr))
}

// listenAddr is --addr when given, else the D26 default for where the binary runs.
func listenAddr(flagAddr string, inCluster bool) string {
	switch {
	case flagAddr != "":
		return flagAddr
	case inCluster:
		return inClusterAddr
	default:
		return localAddr
	}
}

// serveHTTP serves until ctx ends, then shuts down. Request contexts derive from ctx, so open
// SSE streams end with it instead of holding Shutdown for its whole timeout.
func serveHTTP(ctx context.Context, ln net.Listener, h http.Handler) error {
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		BaseContext:       func(net.Listener) context.Context { return ctx },
		// No WriteTimeout: /api/stream responses last as long as the page is open.
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		return fmt.Errorf("shutting down: %w", err)
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// startReplay loads the recording and plays it into a hub; no cluster is contacted (D14).
func startReplay(ctx context.Context, path string, log *slog.Logger) (app, error) {
	f, err := os.Open(path)
	if err != nil {
		return app{}, fmt.Errorf("opening recording: %w", err)
	}
	defer func() { _ = f.Close() }() // read-only file; a close error changes nothing
	rec, err := stream.LoadRecording(f)
	if err != nil {
		return app{}, fmt.Errorf("loading recording %s: %w", path, err)
	}
	hub := stream.NewHub(stream.HubOptions{})
	go func() {
		if err := rec.Play(ctx, hub); err != nil {
			log.Error("replay failed", "error", err.Error())
		}
	}()
	srv := &stream.Server{Hub: hub, Ready: func() bool { return true },
		Objects: stream.ReplayObjects{Hub: hub}, Logs: stream.ReplayLogs{Hub: hub}, Log: log}
	return app{handler: srv.Handler()}, nil
}

// startLive probes the sources, starts the watches into the model and the model's deltas into
// the hub, starts the metrics collector, and returns the handler. Only a config or discovery
// failure is an error; every other source degrades to its capability state (R4).
func startLive(ctx context.Context, kubeconfig string, log *slog.Logger) (app, error) {
	cl, err := newClients(kubeconfig, log)
	if err != nil {
		return app{}, err
	}
	cfg, inCluster, cs := cl.cfg, cl.inCluster, cl.cs
	kc, kerr := kubeletClient(cfg, inCluster)
	caps := probeCapabilities(ctx, cs, kc, kerr)

	hub := stream.NewHub(stream.HubOptions{})
	m := model.New()
	if err := hub.Publish(m.SnapshotEvent(caps)); err != nil {
		return app{}, err
	}
	w := watch.New(cs.Discovery(), cl.watchDyn, func(batch []model.Change) {
		if err := hub.Publish(model.DeltaEvents(m.Apply(batch...))...); err != nil {
			log.Error("publishing model deltas failed", "error", err.Error())
		}
	}, log)
	if err := w.Start(ctx); err != nil {
		return app{}, fmt.Errorf("starting watches: %w", err)
	}
	col := sources.New(sources.Deps{Model: m, Raw: sources.RESTRaw{Client: cs.Discovery().RESTClient()},
		Kubelet: kc, KubeletErr: kerr, Out: hub, Log: log, Initial: caps, Ready: w.Synced})
	go col.Run(ctx, sources.DefaultInterval)

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(cs.Discovery()))
	srv := &stream.Server{Hub: hub, Ready: w.Synced, Log: log,
		Objects: stream.ClusterObjects{Model: m, Dyn: cl.dyn, Mapper: mapper},
		Logs:    stream.ClusterLogs{Model: m, Client: cs}}
	log.Info("watching", "host", cfg.Host, "inCluster", inCluster)
	return app{handler: srv.Handler(), inCluster: inCluster}, nil
}

// clients are the API clients of a live run.
type clients struct {
	cfg       *rest.Config // carries requestTimeout
	inCluster bool
	cs        kubernetes.Interface
	dyn       dynamic.Interface // object reads for /api/objects
	watchDyn  dynamic.Interface // informers only: no client timeout
}

func newClients(kubeconfig string, log *slog.Logger) (clients, error) {
	cfg, inCluster, err := restConfig(kubeconfig)
	if err != nil {
		return clients{}, fmt.Errorf("loading cluster config: %w", err)
	}
	cfg.WarningHandler = &apiWarnings{log: log}
	// Watches must not carry a client timeout: it would end every watch after that long.
	watchCfg := rest.CopyConfig(cfg)
	cfg.Timeout = requestTimeout
	cl := clients{cfg: cfg, inCluster: inCluster}
	if cl.cs, err = kubernetes.NewForConfig(cfg); err != nil {
		return clients{}, fmt.Errorf("building client: %w", err)
	}
	if cl.dyn, err = dynamic.NewForConfig(cfg); err != nil {
		return clients{}, fmt.Errorf("building dynamic client: %w", err)
	}
	if cl.watchDyn, err = dynamic.NewForConfig(watchCfg); err != nil {
		return clients{}, fmt.Errorf("building watch client: %w", err)
	}
	return cl, nil
}

// apiWarnings logs each distinct API warning header once as JSON. client-go's default handler
// writes plain klog lines to stderr on every relist, which breaks the one-JSON-line-per-entry
// log contract and repeats deprecation notices (v1 Endpoints) for as long as the app runs.
type apiWarnings struct {
	log  *slog.Logger
	seen sync.Map
}

func (w *apiWarnings) HandleWarningHeader(code int, _ string, text string) {
	if code != 299 || text == "" {
		return
	}
	if _, dup := w.seen.LoadOrStore(text, struct{}{}); dup {
		return
	}
	w.log.Warn("api warning", "text", text)
}

// probeCapabilities runs the startup probe for the snapshot's capability list.
func probeCapabilities(ctx context.Context, cs kubernetes.Interface, kc probe.SummaryFetcher, kerr error) map[string]model.Capability {
	results := probe.Run(ctx, probe.Deps{
		Client:     cs,
		Raw:        probe.RESTRaw{Client: cs.Discovery().RESTClient()},
		Kubelet:    kc,
		KubeletErr: kerr,
		Dial:       (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
	})
	caps := make(map[string]model.Capability, len(results))
	for _, r := range results {
		caps[r.Source] = model.Capability{State: string(r.State), Detail: r.Detail}
	}
	return caps
}
