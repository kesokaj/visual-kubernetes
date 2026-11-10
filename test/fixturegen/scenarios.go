package main

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	mt "github.com/kesokaj/visual-kubernetes/internal/model/modeltest"
)

// scenarios returns the four recorded streams, in file-name order.
func scenarios() []scenario {
	return []scenario{deployScale(), largeCluster(), missingSources(), nodeJoin()}
}

func nodeNames(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("node-%d", i+1)
	}
	return out
}

func nodes(names []string) []runtime.Object {
	out := make([]runtime.Object, len(names))
	for i, n := range names {
		out[i] = mt.Node(n, true)
	}
	return out
}

// deployment is a Deployment, its ReplicaSet and replicas Running, ready pods spread
// round-robin over nodes starting at offset.
type deployment struct {
	d    *appsv1.Deployment
	rs   *appsv1.ReplicaSet
	pods []*corev1.Pod
}

func newDeployment(ns, name string, replicas int, nodes []string, offset int, configMaps ...string) deployment {
	d := mt.Deployment(ns, name, int32(replicas), mt.Labels("app", name), configMaps...)
	rs := mt.ReplicaSet(d, "7d4b9c")
	out := deployment{d: d, rs: rs}
	for i := range replicas {
		out.pods = append(out.pods, running(mt.PodOf(rs, podSuffix(i)), nodes[(offset+i)%len(nodes)]))
	}
	return out
}

func (d deployment) objects() []runtime.Object {
	out := []runtime.Object{d.d, d.rs}
	for _, p := range d.pods {
		out = append(out, p)
	}
	return out
}

func podSuffix(i int) string { return fmt.Sprintf("x%03d", i) }

func running(p *corev1.Pod, node string) *corev1.Pod { return mt.Running(mt.Scheduled(p, node), true) }

// daemonSet is a DaemonSet with one Running pod per node.
func daemonSet(ns, name string, nodes []string) []runtime.Object {
	ds := mt.DaemonSet(ns, name, mt.Labels("k8s-app", name), int32(len(nodes)))
	out := []runtime.Object{ds}
	for _, n := range nodes {
		out = append(out, daemonPod(ds, n))
	}
	return out
}

func daemonPod(ds *appsv1.DaemonSet, node string) *corev1.Pod {
	p := mt.Pod(ds.Namespace, ds.Name+"-"+node, ds.Spec.Template.Labels, mt.ControllerRef("apps/v1", "DaemonSet", ds.Name))
	return running(p, node)
}

// deployScale: an HPA raises web from 2 to 4 replicas; the new pods go Pending, are
// scheduled, then become ready.
func deployScale() scenario {
	ns := nodeNames(3)
	web := newDeployment("shop", "web", 2, ns, 0, "web-config")
	initial := append(nodes(ns), daemonSet("kube-system", "kube-proxy", ns)...)
	initial = append(initial, web.objects()...)
	initial = append(initial,
		mt.Service("shop", "web", mt.Labels("app", "web")),
		mt.ConfigMap("shop", "web-config"),
		mt.HPA("shop", "web", "Deployment", "web", 2, 6, 2),
	)
	scaled := mt.Scaled(web.d, 4)
	rs := mt.ReplicaSet(scaled, "7d4b9c")
	fresh := []*corev1.Pod{mt.PodOf(rs, podSuffix(2)), mt.PodOf(rs, podSuffix(3))}
	return scenario{name: "deploy-scale", caps: capabilities(nil), steps: []step{
		{0, upserts(initial...)},
		{1000, upserts(mt.HPA("shop", "web", "Deployment", "web", 2, 6, 4), scaled, rs)},
		{1500, upserts(fresh[0], fresh[1])},
		{2500, upserts(mt.Scheduled(fresh[0], "node-2"), mt.Scheduled(fresh[1], "node-3"))},
		{4000, upserts(running(fresh[0], "node-2"), running(fresh[1], "node-3"))},
	}}
}

// nodeJoin: a third node registers NotReady, turns Ready, and kube-proxy's DaemonSet
// places a pod on it.
func nodeJoin() scenario {
	ns := nodeNames(2)
	initial := append(nodes(ns), daemonSet("kube-system", "kube-proxy", ns)...)
	initial = append(initial, newDeployment("shop", "web", 3, ns, 0).objects()...)
	initial = append(initial, mt.Service("shop", "web", mt.Labels("app", "web")))
	ds := mt.DaemonSet("kube-system", "kube-proxy", mt.Labels("k8s-app", "kube-proxy"), 3)
	pending := mt.Pod("kube-system", "kube-proxy-node-3", ds.Spec.Template.Labels, mt.ControllerRef("apps/v1", "DaemonSet", ds.Name))
	return scenario{name: "node-join", caps: capabilities(nil), steps: []step{
		{0, upserts(initial...)},
		{1000, upserts(mt.Node("node-3", false))},
		{3000, upserts(mt.Node("node-3", true), ds)},
		{3500, upserts(mt.Scheduled(pending, "node-3"))},
		{5000, upserts(running(pending, "node-3"))},
	}}
}

// missingSources: a small cluster whose probe found no metrics API, an unreachable kubelet,
// denied API server metrics and no Hubble; one Deployment arrives so deltas still flow.
func missingSources() scenario {
	ns := nodeNames(1)
	initial := append(nodes(ns), newDeployment("shop", "web", 1, ns, 0).objects()...)
	api := newDeployment("shop", "api", 2, ns, 0)
	caps := capabilities(map[string]model.Capability{
		"metrics.k8s.io":    {State: "missing", Detail: "metrics.k8s.io not served"},
		"kubelet":           {State: "unreachable", Detail: "local mode: node addresses not routable"},
		"apiserver-metrics": {State: "denied", Detail: "403 on /metrics"},
	})
	return scenario{name: "missing-sources", caps: caps, steps: []step{
		{0, upserts(initial...)},
		{1000, upserts(api.objects()...)},
		{2000, deletes(api.pods[1])},
	}}
}
