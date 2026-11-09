package kubelet

import "time"

// Summary is the subset of the kubelet stats/v1alpha1 Summary the visualizer reads: node and pod
// network bytes for throughput sizing (PRD S2) and CPU/memory for the fallback. Declared here
// rather than importing k8s.io/kubelet to keep the dependency set small; unknown fields are ignored.
type Summary struct {
	Node NodeStats  `json:"node"`
	Pods []PodStats `json:"pods"`
}

// NodeStats holds node-level usage.
type NodeStats struct {
	NodeName string        `json:"nodeName"`
	CPU      *CPUStats     `json:"cpu,omitempty"`
	Memory   *MemoryStats  `json:"memory,omitempty"`
	Network  *NetworkStats `json:"network,omitempty"`
}

// PodStats holds one pod's usage.
type PodStats struct {
	PodRef  PodReference  `json:"podRef"`
	CPU     *CPUStats     `json:"cpu,omitempty"`
	Memory  *MemoryStats  `json:"memory,omitempty"`
	Network *NetworkStats `json:"network,omitempty"`
}

// PodReference names a pod.
type PodReference struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	UID       string `json:"uid"`
}

// CPUStats is cumulative and instantaneous CPU use.
type CPUStats struct {
	Time                 time.Time `json:"time"`
	UsageNanoCores       *uint64   `json:"usageNanoCores,omitempty"`
	UsageCoreNanoSeconds *uint64   `json:"usageCoreNanoSeconds,omitempty"`
}

// MemoryStats is the working set.
type MemoryStats struct {
	Time            time.Time `json:"time"`
	WorkingSetBytes *uint64   `json:"workingSetBytes,omitempty"`
}

// NetworkStats is cumulative bytes on the default interface.
type NetworkStats struct {
	Time    time.Time `json:"time"`
	RxBytes *uint64   `json:"rxBytes,omitempty"`
	TxBytes *uint64   `json:"txBytes,omitempty"`
}
