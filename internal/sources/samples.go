package sources

import (
	"slices"
	"strings"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	"github.com/kesokaj/visual-kubernetes/internal/stream"
)

// rateFunc is rateTable.rate, injected so tests can drive buildMetrics directly.
type rateFunc func(id string) (rx, tx float64, ok bool)

// appSum accumulates one app's usage from its pods.
type appSum struct {
	rx, tx         float64
	hasNet         bool
	cpu, mem       int64
	hasCPU, hasMem bool
}

// buildMetrics makes the `metrics` event: one sample per drawn pod with the values that exist,
// one per app summing its pods, sorted by id. sizeBy falls back from network to CPU to pod
// count by what any pod has this cycle (PRD S2), so the whole page sizes by one measure.
func buildMetrics(ents []model.Entity, use map[string]usage, rate rateFunc) stream.MetricsData {
	apps := map[string]*appSum{}
	samples := []stream.Sample{}
	anyNet, anyCPU := false, false
	for _, e := range ents {
		if e.Kind != "Pod" || e.Group != model.CoreGroup || e.Folded {
			continue
		}
		s := stream.Sample{ID: e.ID}
		sum := apps[e.AppID]
		if e.AppID != "" && sum == nil {
			sum = &appSum{}
			apps[e.AppID] = sum
		}
		if rx, tx, ok := rate(e.ID); ok {
			s.RxBps, s.TxBps = &rx, &tx
			anyNet = true
			if sum != nil {
				sum.rx, sum.tx, sum.hasNet = sum.rx+rx, sum.tx+tx, true
			}
		}
		if u, ok := use[e.ID]; ok {
			s.CPUMilli, s.MemBytes = &u.cpuMilli, &u.memBytes
			anyCPU = true
			if sum != nil {
				sum.cpu, sum.mem, sum.hasCPU, sum.hasMem = sum.cpu+u.cpuMilli, sum.mem+u.memBytes, true, true
			}
		}
		if s.RxBps != nil || s.CPUMilli != nil {
			samples = append(samples, s)
		}
	}
	for id, sum := range apps {
		if s, ok := sum.sample(id); ok {
			samples = append(samples, s)
		}
	}
	slices.SortFunc(samples, func(a, b stream.Sample) int { return strings.Compare(a.ID, b.ID) })
	sizeBy := stream.SizeByPods
	switch {
	case anyNet:
		sizeBy = stream.SizeByNetwork
	case anyCPU:
		sizeBy = stream.SizeByCPU
	}
	return stream.MetricsData{SizeBy: sizeBy, Samples: samples}
}

func (a *appSum) sample(id string) (stream.Sample, bool) {
	s := stream.Sample{ID: id}
	if a.hasNet {
		s.RxBps, s.TxBps = &a.rx, &a.tx
	}
	if a.hasCPU {
		s.CPUMilli = &a.cpu
	}
	if a.hasMem {
		s.MemBytes = &a.mem
	}
	return s, a.hasNet || a.hasCPU || a.hasMem
}
