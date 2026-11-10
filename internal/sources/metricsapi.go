package sources

import (
	"context"
	"encoding/json"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	"github.com/kesokaj/visual-kubernetes/internal/probe"
)

// podMetricsPath lists every pod's usage from metrics-server (or any metrics.k8s.io adapter).
const podMetricsPath = "/apis/metrics.k8s.io/v1beta1/pods"

// usage is one pod's summed container usage from metrics.k8s.io.
type usage struct {
	cpuMilli int64
	memBytes int64
}

// podMetricsList is the part of a PodMetricsList the collector reads. The k8s.io/metrics
// types are not imported for three fields.
type podMetricsList struct {
	Items []struct {
		Metadata struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		Containers []struct {
			Usage map[string]string `json:"usage"`
		} `json:"containers"`
	} `json:"items"`
}

// podMetrics reads every pod's CPU and memory, keyed by pod entity id. The ok detail stays the
// same from cycle to cycle so a healthy source never emits a capabilities event.
func (c *Collector) podMetrics(ctx context.Context) (map[string]usage, model.Capability) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	body, err := c.d.Raw.Get(ctx, podMetricsPath, nil)
	if apierrors.IsNotFound(err) {
		return nil, model.Capability{State: string(probe.Missing), Detail: "metrics.k8s.io/v1beta1 not served (no metrics-server?)"}
	}
	if err != nil {
		return nil, capability(err)
	}
	out, err := parsePodMetrics(body)
	if err != nil {
		return nil, model.Capability{State: string(probe.Error), Detail: detail(err.Error())}
	}
	return out, model.Capability{State: string(probe.OK), Detail: "v1beta1 pods"}
}

// parsePodMetrics sums container usage per pod. A quantity that does not parse counts as zero:
// one odd container must not hide the rest of the cluster.
func parsePodMetrics(body []byte) (map[string]usage, error) {
	var list podMetricsList
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("decoding PodMetricsList: %w", err)
	}
	out := make(map[string]usage, len(list.Items))
	for _, it := range list.Items {
		var cpu, mem resource.Quantity // summed exactly, rounded once
		for _, ctr := range it.Containers {
			if q, err := resource.ParseQuantity(ctr.Usage["cpu"]); err == nil {
				cpu.Add(q)
			}
			if q, err := resource.ParseQuantity(ctr.Usage["memory"]); err == nil {
				mem.Add(q)
			}
		}
		id := model.ID(model.CoreGroup, "Pod", it.Metadata.Namespace, it.Metadata.Name)
		out[id] = usage{cpuMilli: cpu.MilliValue(), memBytes: mem.Value()}
	}
	return out, nil
}
