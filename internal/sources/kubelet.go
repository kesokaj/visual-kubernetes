package sources

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/kesokaj/visual-kubernetes/internal/kubelet"
	"github.com/kesokaj/visual-kubernetes/internal/model"
	"github.com/kesokaj/visual-kubernetes/internal/probe"
)

// kubeletParallel bounds concurrent Summary requests, so a large cluster does not open a
// connection to every node at once.
const kubeletParallel = 8

// target is one node's kubelet address.
type target struct {
	node, host string
	port       int
}

// nodeResult is one node's Summary or the error fetching it.
type nodeResult struct {
	s   *kubelet.Summary
	err error
}

// kubeletRates reads every node's Summary and feeds the pods' network counters into the rate
// table. hostNetwork pods are skipped: their counters are the node's own interfaces, which would
// make kube-proxy or a CNI agent look like the busiest app in the cluster (PRD S2).
func (c *Collector) kubeletRates(ctx context.Context, ents []model.Entity, now time.Time) model.Capability {
	defer c.rates.prune(now)
	if c.d.Kubelet == nil {
		err := c.d.KubeletErr
		if errors.Is(err, probe.ErrLocalMode) {
			return model.Capability{State: string(probe.Unreachable), Detail: detail(err.Error())}
		}
		if err == nil {
			err = errors.New("kubelet client not configured")
		}
		return model.Capability{State: string(probe.Error), Detail: detail(err.Error())}
	}
	targets := c.kubeletTargets(ents)
	if len(targets) == 0 {
		return model.Capability{State: string(probe.Missing), Detail: "no node with an InternalIP"}
	}
	results := c.fetchSummaries(ctx, targets)
	okNodes := 0
	var firstErr error
	for i, r := range results {
		if r.err != nil {
			c.d.Log.Debug("kubelet summary failed", "node", targets[i].node, "error", r.err.Error())
			firstErr = cmpOr(firstErr, fmt.Errorf("node %s: %w", targets[i].node, r.err))
			continue
		}
		okNodes++
		c.observeSummary(r.s, now)
	}
	if okNodes == 0 {
		return capability(firstErr)
	}
	return model.Capability{State: string(probe.OK),
		Detail: fmt.Sprintf("%d/%d nodes; TLS verified; hostNetwork pods excluded", okNodes, len(targets))}
}

// cmpOr keeps the first non-nil error.
func cmpOr(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

// kubeletTargets addresses each node in the model by its InternalIP and advertised kubelet
// port, never through the API server's nodes/proxy (R8, ADR-005).
func (c *Collector) kubeletTargets(ents []model.Entity) []target {
	var out []target
	for _, e := range ents {
		if e.Kind != "Node" || e.Group != model.CoreGroup {
			continue
		}
		obj, ok := c.d.Model.Object(e.ID)
		node, isNode := obj.(*corev1.Node)
		if !ok || !isNode {
			continue
		}
		for _, a := range node.Status.Addresses {
			if a.Type == corev1.NodeInternalIP && a.Address != "" {
				out = append(out, target{node: node.Name, host: a.Address, port: int(node.Status.DaemonEndpoints.KubeletEndpoint.Port)})
				break
			}
		}
	}
	return out
}

// fetchSummaries reads the targets concurrently, each bounded by fetchTimeout.
func (c *Collector) fetchSummaries(ctx context.Context, targets []target) []nodeResult {
	results := make([]nodeResult, len(targets))
	sem := make(chan struct{}, kubeletParallel)
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
			defer cancel()
			s, err := c.d.Kubelet.Summary(ctx, t.host, t.port)
			results[i] = nodeResult{s: s, err: err}
		})
	}
	wg.Wait()
	return results
}

// observeSummary records each known, pod-network pod's counters. Pods the model does not hold
// (just deleted, or not yet listed) are skipped.
func (c *Collector) observeSummary(s *kubelet.Summary, now time.Time) {
	if s == nil {
		return
	}
	for _, p := range s.Pods {
		if p.Network == nil || p.Network.RxBytes == nil || p.Network.TxBytes == nil {
			continue
		}
		id := model.ID(model.CoreGroup, "Pod", p.PodRef.Namespace, p.PodRef.Name)
		obj, ok := c.d.Model.Object(id)
		pod, isPod := obj.(*corev1.Pod)
		if !ok || !isPod || pod.Spec.HostNetwork {
			continue
		}
		t := p.Network.Time
		if t.IsZero() {
			t = now
		}
		c.rates.observe(id, p.PodRef.UID, reading{t: t, rx: *p.Network.RxBytes, tx: *p.Network.TxBytes}, now)
	}
}
