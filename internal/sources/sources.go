// Package sources polls the metrics sources every few seconds and turns them into stream
// events: per-pod and per-app usage (`metrics`, sized by network throughput from kubelet
// stats with hostNetwork pods excluded, falling back to CPU, then pod count; PRD S2), the
// inferred control-plane entities from Leases, `/readyz` and `/metrics` (PRD S4), and the
// capability states the API server panel lists (PRD R4).
package sources

import (
	"context"
	"log/slog"
	"maps"
	"net/url"
	"strings"
	"time"
	"unicode"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	"github.com/kesokaj/visual-kubernetes/internal/probe"
	"github.com/kesokaj/visual-kubernetes/internal/stream"
)

// Defaults for the poll loop.
const (
	DefaultInterval = 5 * time.Second
	// RateWindow is the span a throughput rate is measured over (plan: the delta over 10 s).
	RateWindow = 10 * time.Second
	// fetchTimeout bounds each source request so one slow source cannot stall a cycle.
	fetchTimeout = 4 * time.Second
	// metricsEvery is how many cycles pass between reads of the API server's /metrics, which
	// is several MB on a real cluster and only feeds a slow-moving object count.
	metricsEvery = 6
)

// Raw GETs an absolute API server path with query parameters.
type Raw interface {
	Get(ctx context.Context, path string, params url.Values) ([]byte, error)
}

// RESTRaw is the production Raw. Non-2xx answers come back as typed apierrors together with
// the body, so `/readyz?verbose` can still be parsed when the API server is not ready.
type RESTRaw struct{ Client rest.Interface }

// Get implements Raw.
func (r RESTRaw) Get(ctx context.Context, path string, params url.Values) ([]byte, error) {
	req := r.Client.Get().AbsPath(path)
	for k, vs := range params {
		for _, v := range vs {
			req = req.Param(k, v)
		}
	}
	return req.DoRaw(ctx)
}

// ModelReader is the part of the live model the sources read.
type ModelReader interface {
	Object(id string) (runtime.Object, bool)
	Snapshot() ([]model.Entity, []model.Edge)
}

// Publisher receives the events of each cycle; the stream hub in production.
type Publisher interface {
	Publish(events ...model.Event) error
}

// Deps are what the collector reads and where it writes.
type Deps struct {
	Model ModelReader
	Raw   Raw
	// Kubelet is nil when it could not or must not be built; KubeletErr says why
	// (probe.ErrLocalMode outside the cluster, ADR-006).
	Kubelet    probe.SummaryFetcher
	KubeletErr error
	Out        Publisher
	Log        *slog.Logger
	// Initial is the startup probe's report; the collector updates the sources it polls.
	Initial map[string]model.Capability
	// Now is the clock; time.Now when nil.
	Now func() time.Time
	// Ready gates the cycles until it first reports true; nil means always. Production passes
	// the watch's Synced: a cycle over the empty pre-sync model would publish false states.
	Ready func() bool
}

// Collector runs the poll cycles. It is not safe for concurrent use: Run owns it.
type Collector struct {
	d       Deps
	rates   rateTable
	caps    map[string]model.Capability
	cp      map[string]model.Entity // control-plane entities as last published
	cpEdges bool                    // control-plane edges published
	objects *int64                  // API server object count from /metrics, nil until read
	cycle   int
}

// New returns a collector; nothing runs until Run or Collect.
func New(d Deps) *Collector {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	caps := maps.Clone(d.Initial)
	if caps == nil {
		caps = map[string]model.Capability{}
	}
	return &Collector{d: d, rates: rateTable{window: RateWindow, h: map[string]*history{}}, caps: caps, cp: map[string]model.Entity{}}
}

// Run collects at once and then every interval, publishing each cycle's events, until ctx
// ends; cycles before Ready are skipped. interval 0 means DefaultInterval.
func (c *Collector) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	// The gate latches: Synced also drops while informer changes are queued, and a skipped
	// cycle mid-demo would freeze the metrics.
	open := c.d.Ready == nil
	for {
		if !open {
			open = c.d.Ready()
		}
		if open {
			if err := c.d.Out.Publish(c.Collect(ctx)...); err != nil {
				c.d.Log.Error("publishing source events failed", "error", err.Error())
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Collect runs one cycle and returns its events in order: control-plane edges and upserts that
// changed, a `capabilities` event when a state changed, then the `metrics` event.
func (c *Collector) Collect(ctx context.Context) []model.Event {
	now := c.d.Now()
	ents, _ := c.d.Model.Snapshot()
	usage, metricsCap := c.podMetrics(ctx)
	c.caps["metrics.k8s.io"] = metricsCap
	c.caps["kubelet"] = c.kubeletRates(ctx, ents, now)
	rz, readyCap := c.readyz(ctx)
	c.caps["readyz"] = readyCap
	if c.cycle%metricsEvery == 0 {
		c.caps["apiserver-metrics"] = c.apiserverMetrics(ctx)
	}
	c.cycle++
	events := c.controlPlaneEvents(c.controlPlane(now, rz))
	events = append(events, c.capabilitiesEvent()...)
	return append(events, model.Event{Type: stream.EventMetrics, Data: buildMetrics(ents, usage, c.rates.rate)})
}

// capabilitiesEvent returns a `capabilities` event when any state or detail differs from what
// clients last saw (the startup probe's report in the snapshot, then the last such event);
// d.Initial tracks that last-sent set.
func (c *Collector) capabilitiesEvent() []model.Event {
	if maps.Equal(c.caps, c.d.Initial) {
		return nil
	}
	c.d.Initial = maps.Clone(c.caps)
	return []model.Event{{Type: stream.EventCapabilities, Data: stream.CapabilitiesData{Capabilities: maps.Clone(c.caps)}}}
}

// capability turns a source error into its state, as the probe would print it.
func capability(err error) model.Capability {
	return model.Capability{State: string(probe.Classify(err)), Detail: detail(err.Error())}
}

const maxDetail = 240

// detail keeps a capability detail one short line without control characters: it reaches the
// browser from API and kubelet error text.
func detail(s string) string {
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
