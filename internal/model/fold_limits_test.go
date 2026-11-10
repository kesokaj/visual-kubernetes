package model_test

import (
	"fmt"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	mt "github.com/kesokaj/visual-kubernetes/internal/model/modeltest"
)

// barePods is n owner-less pods in shop: n apps of one pod each, no nodes.
func barePods(n int) []runtime.Object {
	var objs []runtime.Object
	for i := range n {
		objs = append(objs, mt.Pod("shop", fmt.Sprintf("p-%03d", i), nil, nil))
	}
	return objs
}

// bigDeployments is count Deployments in shop with replicas pods each, no nodes.
func bigDeployments(count, replicas int) []runtime.Object {
	var objs []runtime.Object
	for i := range count {
		d := mt.Deployment("shop", fmt.Sprintf("big-%d", i), int32(replicas), mt.Labels("app", fmt.Sprintf("big-%d", i)))
		rs := mt.ReplicaSet(d, "h")
		objs = append(objs, d, rs)
		for p := range replicas {
			objs = append(objs, mt.PodOf(rs, fmt.Sprint(p)))
		}
	}
	return objs
}

// manyNodes is n nodes with a kube-system DaemonSet pod on each, plus a 3-pod user app.
func manyNodes(n int) []runtime.Object {
	ds := mt.DaemonSet("kube-system", "kube-proxy", mt.Labels("k8s-app", "kube-proxy"), int32(n))
	objs := []runtime.Object{ds}
	for i := range n {
		node := fmt.Sprintf("node-%02d", i)
		objs = append(objs, mt.Node(node, true),
			mt.Pod("kube-system", "kube-proxy-"+node, ds.Spec.Template.Labels, mt.ControllerRef("apps/v1", "DaemonSet", "kube-proxy")))
	}
	return append(objs, bigDeployments(1, 3)...)
}

// TestFoldingPerLimit trips each S5 limit on its own, so every clause of the fold gate and
// loop has a case that fails without it.
func TestFoldingPerLimit(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		objs                  []runtime.Object
		wantPods, wantApps    int
		wantFolded, wantDrawn string
	}{
		{"apps only", barePods(model.BubbleLimit + 50), model.BubbleLimit, model.BubbleLimit, "app/shop/Pod/p-000", "app/shop/Pod/p-199"},
		{"pods only", bigDeployments(3, 200), 400, 2, "app/shop/Deployment/big-0", "app/shop/Deployment/big-2"},
		{"nodes only", manyNodes(model.NodeLimit + 5), 3, 1, "app/kube-system/DaemonSet/kube-proxy", "app/shop/Deployment/big-0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := model.New()
			apply(m, tc.objs...)
			ents := entities(m)
			pods, apps := 0, 0
			for _, e := range ents {
				switch {
				case e.Folded:
				case strings.HasPrefix(e.ID, "app/"): // before Kind: a bare pod's app has kind Pod
					apps++
				case e.Kind == "Pod":
					pods++
				}
			}
			if pods != tc.wantPods || apps != tc.wantApps {
				t.Errorf("drawn pods, apps = %d, %d; want %d, %d", pods, apps, tc.wantPods, tc.wantApps)
			}
			if !ents[tc.wantFolded].Folded || ents[tc.wantDrawn].Folded {
				t.Errorf("%s folded=%v, %s folded=%v; want only the first folded",
					tc.wantFolded, ents[tc.wantFolded].Folded, tc.wantDrawn, ents[tc.wantDrawn].Folded)
			}
		})
	}
}
