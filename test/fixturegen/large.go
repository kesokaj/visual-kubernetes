package main

import (
	"fmt"
	"slices"

	"k8s.io/apimachinery/pkg/runtime"

	mt "github.com/kesokaj/visual-kubernetes/internal/model/modeltest"
)

const (
	largeNodes = 25
	largePods  = 600
)

// largeCluster: 25 nodes and 600 pods, past every S5 limit, so the snapshot already holds
// the folded counters; node-26 then joins and its DaemonSet pods land in the folded system
// apps, and the first user app scales up by two pods.
func largeCluster() scenario {
	ns := nodeNames(largeNodes)
	initial := nodes(ns)
	initial = append(initial, daemonSet("kube-system", "kube-proxy", ns)...)
	initial = append(initial, daemonSet("logging-system", "fluent-bit", ns)...)
	initial = append(initial, newDeployment("kube-system", "coredns", 2, ns, 0).objects()...)
	initial = append(initial, newDeployment("kube-system", "metrics-server", 1, ns, 2).objects()...)
	system := 2*largeNodes + 2 + 1

	user, first := userApps(ns, largePods-system)
	initial = append(initial, user...)

	all := append(slices.Clone(ns), "node-26")
	proxy := mt.DaemonSet("kube-system", "kube-proxy", mt.Labels("k8s-app", "kube-proxy"), largeNodes+1)
	fluent := mt.DaemonSet("logging-system", "fluent-bit", mt.Labels("k8s-app", "fluent-bit"), largeNodes+1)
	scaled := newDeployment(first.d.Namespace, first.d.Name, len(first.pods)+2, all, 0)
	return scenario{name: "large-cluster", caps: capabilities(nil), steps: []step{
		{0, upserts(initial...)},
		{2000, upserts(mt.Node("node-26", false))},
		{3000, upserts(mt.Node("node-26", true), proxy, fluent)},
		{4000, upserts(daemonPod(proxy, "node-26"), daemonPod(fluent, "node-26"))},
		{6000, upserts(scaled.objects()...)},
	}}
}

// userApps returns Deployments with a Service each over four team namespaces holding pods
// pods in total, sizes cycling 1..9, and the first of them (svc-000).
func userApps(nodes []string, pods int) ([]runtime.Object, deployment) {
	var out []runtime.Object
	var first deployment
	for i := 0; pods > 0; i++ {
		replicas := min(pods, 1+(i*7)%9)
		pods -= replicas
		ns := fmt.Sprintf("team-%c", 'a'+i%4)
		name := fmt.Sprintf("svc-%03d", i)
		d := newDeployment(ns, name, replicas, nodes, i)
		if i == 0 {
			first = d
		}
		out = append(out, d.objects()...)
		out = append(out, mt.Service(ns, name, mt.Labels("app", name)))
	}
	return out, first
}
