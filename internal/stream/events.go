package stream

import (
	"github.com/kesokaj/visual-kubernetes/internal/model"
)

// SSE event names this package handles beyond the model's delta events (plan Interfaces
// § SSE events, D24).
const (
	EventSnapshot     = "snapshot"
	EventCapabilities = "capabilities"
	EventMetrics      = "metrics"
)

// CapabilitiesData is the `capabilities` event's data: every source's state, sent whenever one
// changes after the snapshot (D24), so the API server panel and the size fallback stay current.
type CapabilitiesData struct {
	Capabilities map[string]model.Capability `json:"capabilities"`
}

// SizeBy names the measure app bubbles are sized by, chosen by the sources' fallback (PRD S2,
// plan D11): network throughput, else CPU, else pod count.
type SizeBy string

// The three size measures, in fallback order.
const (
	SizeByNetwork SizeBy = "network"
	SizeByCPU     SizeBy = "cpu"
	SizeByPods    SizeBy = "pods"
)

// Sample is one entity's usage; a field is absent when its source has no value for the entity
// (a hostNetwork pod never carries rxBps/txBps).
type Sample struct {
	ID       string   `json:"id"`
	RxBps    *float64 `json:"rxBps,omitempty"`
	TxBps    *float64 `json:"txBps,omitempty"`
	CPUMilli *int64   `json:"cpuMilli,omitempty"`
	MemBytes *int64   `json:"memBytes,omitempty"`
}

// MetricsData is the `metrics` event's data, sent every 5 s.
type MetricsData struct {
	SizeBy  SizeBy   `json:"sizeBy"`
	Samples []Sample `json:"samples"`
}
