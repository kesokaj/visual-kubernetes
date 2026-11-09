package probe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func probeDiscovery(ctx context.Context, d Deps) (string, error) {
	v, err := d.Client.Discovery().ServerVersion()
	if err != nil {
		return "", err
	}
	groups, err := d.Client.Discovery().ServerGroups()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s, %d API groups", v.GitVersion, len(groups.Groups)), nil
}

// controlPlaneLeases are the leader-election leases the inferred control plane reads (PRD S4).
var controlPlaneLeases = []string{"kube-scheduler", "kube-controller-manager"}

func probeLeases(ctx context.Context, d Deps) (string, error) {
	list, err := d.Client.CoordinationV1().Leases("kube-system").List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", err
	}
	have := map[string]bool{}
	for _, l := range list.Items {
		have[l.Name] = true
	}
	var found []string
	for _, n := range controlPlaneLeases {
		if have[n] {
			found = append(found, n)
		}
	}
	if len(found) == 0 {
		return fmt.Sprintf("%d leases in kube-system, no control-plane leader leases (managed control plane?)", len(list.Items)), nil
	}
	return fmt.Sprintf("%d leases in kube-system; leader leases: %s", len(list.Items), strings.Join(found, ", ")), nil
}

func probeEvents(ctx context.Context, d Deps) (string, error) {
	list, err := d.Client.EventsV1().Events("").List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("events.k8s.io/v1 list ok (%d sampled)", len(list.Items)), nil
}

func probeEndpointSlices(ctx context.Context, d Deps) (string, error) {
	list, err := d.Client.DiscoveryV1().EndpointSlices("").List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("discovery.k8s.io/v1 list ok (%d sampled)", len(list.Items)), nil
}

func probeMetricsAPI(ctx context.Context, d Deps) (string, error) {
	body, err := d.Raw.Get(ctx, "/apis/metrics.k8s.io/v1beta1/nodes")
	if err != nil {
		if apierrors.IsNotFound(err) {
			return "", stateError{Missing, "metrics.k8s.io/v1beta1 not served (no metrics-server?)"}
		}
		return "", err
	}
	var list struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return "", fmt.Errorf("decoding NodeMetricsList: %w", err)
	}
	return fmt.Sprintf("%d node metrics", len(list.Items)), nil
}

func probeKubelet(ctx context.Context, d Deps) (string, error) {
	if d.Kubelet == nil {
		err := d.KubeletErr
		if errors.Is(err, ErrLocalMode) {
			return "", stateError{Unreachable, err.Error()}
		}
		if err == nil {
			err = errors.New("kubelet client not configured")
		}
		return "", stateError{Error, err.Error()}
	}
	nodes, err := d.Client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", err
	}
	node, host, ok := firstAddressedNode(nodes.Items)
	if !ok {
		return "", stateError{Missing, "no node with an InternalIP"}
	}
	port := int(node.Status.DaemonEndpoints.KubeletEndpoint.Port)
	s, err := d.Kubelet.Summary(ctx, host, port)
	if err != nil {
		return "", err
	}
	withNet := 0
	for _, p := range s.Pods {
		if p.Network != nil && p.Network.RxBytes != nil {
			withNet++
		}
	}
	return fmt.Sprintf("node %s (%s): summary ok, %d pods, %d with network bytes; TLS verified",
		node.Name, net.JoinHostPort(host, strconv.Itoa(portOrDefault(port))), len(s.Pods), withNet), nil
}

func portOrDefault(p int) int {
	if p == 0 {
		return 10250
	}
	return p
}

func firstAddressedNode(nodes []corev1.Node) (corev1.Node, string, bool) {
	for _, n := range nodes {
		for _, a := range n.Status.Addresses {
			if a.Type == corev1.NodeInternalIP && a.Address != "" {
				return n, a.Address, true
			}
		}
	}
	return corev1.Node{}, "", false
}

// objectCountMetrics are the API server's per-resource object gauges, newest name first
// (apiserver_storage_objects was renamed apiserver_resource_objects).
var objectCountMetrics = []string{"apiserver_resource_objects", "apiserver_storage_objects"}

func probeAPIServerMetrics(ctx context.Context, d Deps) (string, error) {
	body, err := d.Raw.Get(ctx, "/metrics")
	if err != nil {
		return "", err
	}
	for _, m := range objectCountMetrics {
		if bytes.Contains(body, []byte("\n"+m+"{")) {
			return m + " present", nil
		}
	}
	return "", stateError{Missing, "/metrics served without an object-count gauge"}
}

func probeReadyz(ctx context.Context, d Deps) (string, error) {
	body, err := d.Raw.Get(ctx, "/readyz")
	if err != nil {
		return "", err
	}
	if s := strings.TrimSpace(string(body)); s != "ok" {
		return "", stateError{Error, "/readyz answered " + s}
	}
	return "ok", nil
}

// hubbleRelays are the known Hubble Relay Services: Cilium's default and GKE Dataplane V2's
// managed observability namespace. The binary only reads them; it never enables Hubble (PRD N4).
var hubbleRelays = []struct{ namespace, name string }{
	{"kube-system", "hubble-relay"},
	{"gke-managed-dpv2-observability", "hubble-relay"},
}

func probeHubble(ctx context.Context, d Deps) (string, error) {
	var tried []string
	for _, r := range hubbleRelays {
		tried = append(tried, r.namespace+"/"+r.name)
		svc, err := d.Client.CoreV1().Services(r.namespace).Get(ctx, r.name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if len(svc.Spec.Ports) == 0 {
			return "", stateError{Error, r.namespace + "/" + r.name + " has no ports"}
		}
		addr := net.JoinHostPort(fmt.Sprintf("%s.%s.svc", r.name, r.namespace), strconv.Itoa(int(svc.Spec.Ports[0].Port)))
		conn, err := d.Dial(ctx, "tcp", addr)
		if err != nil {
			return "", fmt.Errorf("dialing %s: %w", addr, err)
		}
		_ = conn.Close() // reachability check only; nothing was sent
		return "relay reachable at " + addr, nil
	}
	return "", stateError{Missing, "no Service " + strings.Join(tried, " or ")}
}
