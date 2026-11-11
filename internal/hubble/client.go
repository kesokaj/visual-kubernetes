package hubble

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/cilium/cilium/api/v1/flow"
	"github.com/cilium/cilium/api/v1/observer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"k8s.io/client-go/kubernetes"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	"github.com/kesokaj/visual-kubernetes/internal/probe"
)

// Default intervals for Relay aggregation and reconnect backoff.
const (
	DefaultFlushInterval = 1 * time.Second
	DefaultRetryInterval = 5 * time.Second
	dialTimeout          = 5 * time.Second
	maxDetail            = 240
)

// Publisher receives `flow` SSE events.
type Publisher interface {
	Publish(events ...model.Event) error
}

// DialFunc opens a TCP connection to a Relay address.
type DialFunc func(ctx context.Context, addr string) (net.Conn, error)

// Deps configures a Runner.
type Deps struct {
	Client        kubernetes.Interface
	Model         ModelReader
	Out           Publisher
	Log           *slog.Logger
	Ready         func() bool
	OnCapability  func(model.Capability)
	Dial          DialFunc
	HTTPClient    *http.Client
	FlushInterval time.Duration
	RetryInterval time.Duration
}

// Runner streams flows from Hubble Relay (or polls Prometheus when Relay is absent) until ctx ends.
type Runner struct {
	d       Deps
	lastCap model.Capability
}

// New returns a configured Runner; nothing starts until Run is called.
func New(d Deps) *Runner {
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	if d.Dial == nil {
		dialer := &net.Dialer{Timeout: dialTimeout}
		d.Dial = func(ctx context.Context, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp", addr)
		}
	}
	if d.HTTPClient == nil {
		d.HTTPClient = &http.Client{
			Timeout: dialTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errors.New("prometheus redirect refused")
			},
		}
	}
	if d.FlushInterval <= 0 {
		d.FlushInterval = DefaultFlushInterval
	}
	if d.RetryInterval <= 0 {
		d.RetryInterval = DefaultRetryInterval
	}
	return &Runner{d: d}
}

// Run waits for Ready, then auto-detects and streams traffic flows until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) {
	if !r.waitReady(ctx) {
		return
	}
	for ctx.Err() == nil {
		target, err := Detect(ctx, r.d.Client)
		switch {
		case errors.Is(err, ErrMissing):
			r.reportCap(model.Capability{State: string(probe.Missing), Detail: err.Error()})
			r.pollPrometheusOnce(ctx)
		case err != nil:
			r.reportCap(classifyCapability(err))
		default:
			if serr := r.streamRelay(ctx, target); serr != nil && ctx.Err() == nil {
				r.reportCap(classifyCapability(serr))
			}
		}
		if !sleepCtx(ctx, r.d.RetryInterval) {
			return
		}
	}
}

func (r *Runner) waitReady(ctx context.Context) bool {
	for r.d.Ready != nil && !r.d.Ready() {
		if !sleepCtx(ctx, 100*time.Millisecond) {
			return false
		}
	}
	return ctx.Err() == nil
}

func (r *Runner) streamRelay(ctx context.Context, target Target) error {
	creds := insecure.NewCredentials()
	if target.TLS != nil {
		creds = credentials.NewTLS(target.TLS)
	}
	cc, err := grpc.NewClient(
		"passthrough:///"+target.Addr,
		grpc.WithTransportCredentials(creds),
		grpc.WithContextDialer(r.d.Dial),
	)
	if err != nil {
		return err
	}
	defer func() { _ = cc.Close() }()

	if err := waitReadyConn(ctx, cc); err != nil {
		return probeStreamError(ctx, cc, err)
	}
	st, err := observer.NewObserverClient(cc).GetFlows(ctx, &observer.GetFlowsRequest{Follow: true})
	if err != nil {
		return err
	}
	r.reportCap(model.Capability{State: string(probe.OK), Detail: "relay streaming at " + target.Addr})
	return r.consumeStream(ctx, st)
}

func waitReadyConn(ctx context.Context, cc *grpc.ClientConn) error {
	dctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	cc.Connect()
	for {
		s := cc.GetState()
		if s == connectivity.Ready {
			return nil
		}
		if s == connectivity.TransientFailure {
			return errors.New("hubble relay connection failed")
		}
		if !cc.WaitForStateChange(dctx, s) {
			return dctx.Err()
		}
	}
}

func probeStreamError(ctx context.Context, cc *grpc.ClientConn, fallback error) error {
	pctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	_, err := observer.NewObserverClient(cc).GetFlows(pctx, &observer.GetFlowsRequest{Follow: true}, grpc.WaitForReady(false))
	if err != nil {
		return err
	}
	return fallback
}

func (r *Runner) consumeStream(ctx context.Context, st grpc.ServerStreamingClient[observer.GetFlowsResponse]) error {
	ch := make(chan *flow.Flow, 256)
	errc := make(chan error, 1)
	go func() {
		defer close(ch)
		for {
			resp, err := st.Recv()
			if err != nil {
				errc <- err
				return
			}
			if f := resp.GetFlow(); f != nil {
				select {
				case ch <- f:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return r.flushLoop(ctx, ch, errc)
}

func (r *Runner) flushLoop(ctx context.Context, ch <-chan *flow.Flow, errc <-chan error) error {
	agg := NewAggregator()
	var idx *modelIndex
	ticker := time.NewTicker(r.d.FlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case f, ok := <-ch:
			if !ok {
				r.publishFlows(agg.Flush())
				select {
				case err := <-errc:
					if errors.Is(err, io.EOF) {
						return nil
					}
					return err
				default:
					return nil
				}
			}
			if idx == nil {
				idx = newIndex(r.d.Model)
			}
			agg.Add(f, idx)
		case <-ticker.C:
			r.publishFlows(agg.Flush())
			idx = nil
		}
	}
}

func (r *Runner) pollPrometheusOnce(ctx context.Context) {
	baseURL, ok := detectPrometheus(ctx, r.d.Client)
	if !ok {
		return
	}
	flows, err := queryPrometheusFlows(ctx, r.d.HTTPClient, baseURL, newIndex(r.d.Model))
	if err != nil {
		r.d.Log.Debug("prometheus flow query skipped", "error", err.Error())
		return
	}
	r.publishFlows(flows)
}

func (r *Runner) publishFlows(flows []Flow) {
	if len(flows) == 0 {
		return
	}
	events := make([]model.Event, len(flows))
	for i, f := range flows {
		events[i] = model.Event{Type: EventFlow, Data: f}
	}
	if err := r.d.Out.Publish(events...); err != nil {
		r.d.Log.Error("publishing flow events failed", "error", err.Error())
	}
}

func (r *Runner) reportCap(c model.Capability) {
	c.Detail = cleanDetail(c.Detail)
	if c == r.lastCap {
		return
	}
	r.lastCap = c
	if r.d.OnCapability != nil {
		r.d.OnCapability(c)
	}
}

func classifyCapability(err error) model.Capability {
	st := classifyState(err)
	return model.Capability{State: string(st), Detail: err.Error()}
}

func classifyState(err error) probe.State {
	if gst, ok := status.FromError(err); ok {
		msg := gst.Message()
		if strings.Contains(msg, "x509:") || strings.Contains(msg, "tls:") || strings.Contains(msg, "certificate") {
			return probe.Error
		}
		switch gst.Code() {
		case codes.PermissionDenied, codes.Unauthenticated:
			return probe.Denied
		case codes.NotFound, codes.Unimplemented:
			return probe.Missing
		case codes.Unavailable, codes.DeadlineExceeded:
			return probe.Unreachable
		default:
			return probe.Error
		}
	}
	return probe.Classify(err)
}

func cleanDetail(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && !unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > maxDetail {
		s = s[:maxDetail] + "…"
	}
	return s
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
