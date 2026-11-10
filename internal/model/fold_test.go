package model_test

import (
	"fmt"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	mt "github.com/kesokaj/visual-kubernetes/internal/model/modeltest"
)

// bigCluster is 25 nodes, a kube-system DaemonSet with a pod per node, and user Deployments
// in shop sized 1..n pods until the cluster holds total pods.
func bigCluster(total int) []runtime.Object {
	var objs []runtime.Object
	ds := mt.DaemonSet("kube-system", "kube-proxy", mt.Labels("k8s-app", "kube-proxy"), 25)
	objs = append(objs, ds)
	for i := 1; i <= 25; i++ {
		node := fmt.Sprintf("node-%02d", i)
		objs = append(objs, mt.Node(node, true),
			mt.Running(mt.Scheduled(mt.Pod("kube-system", "kube-proxy-"+node, ds.Spec.Template.Labels, mt.ControllerRef("apps/v1", "DaemonSet", "kube-proxy")), node), true))
	}
	left := total - 25
	for app := 1; left > 0; app++ {
		n := min(app, left)
		left -= n
		d := mt.Deployment("shop", fmt.Sprintf("app-%02d", app), int32(n), mt.Labels("app", fmt.Sprintf("app-%02d", app)))
		rs := mt.ReplicaSet(d, "h")
		objs = append(objs, d, rs)
		for p := range n {
			objs = append(objs, mt.Running(mt.Scheduled(mt.PodOf(rs, fmt.Sprint(p)), fmt.Sprintf("node-%02d", p%25+1)), true))
		}
	}
	return objs
}

func TestFoldingBeyondS5Limits(t *testing.T) {
	m := model.New()
	apply(m, bigCluster(600)...)
	ents := entities(m)

	visiblePods, visibleApps, foldedPods := 0, 0, 0
	for _, e := range ents {
		switch {
		case e.Kind == "Pod" && e.Folded:
			foldedPods++
		case e.Kind == "Pod":
			visiblePods++
		case e.Kind != "Counter" && len(e.ID) > 4 && e.ID[:4] == "app/" && !e.Folded:
			visibleApps++
		}
	}
	if visiblePods > model.PodLimit || visiblePods == 0 {
		t.Errorf("visible pods = %d, want 1..%d", visiblePods, model.PodLimit)
	}
	if visibleApps > model.BubbleLimit {
		t.Errorf("visible apps = %d, want ≤ %d", visibleApps, model.BubbleLimit)
	}
	if !ents["app/kube-system/DaemonSet/kube-proxy"].Folded {
		t.Error("kube-system app not folded; system apps fold first")
	}
	if ents["core/Node/_/node-25"].Folded {
		t.Error("a node is folded; nodes never fold")
	}
	if c := ents[model.FoldedPodsID]; c.Count != foldedPods || c.Count != 600-visiblePods {
		t.Errorf("folded pods counter = %d, want %d", c.Count, foldedPods)
	}
	if c := ents[model.FoldedAppsID]; c.Count < 2 {
		t.Errorf("folded apps counter = %d, want the system app and the smallest user apps", c.Count)
	}
	// Smallest first: app-01 (1 pod) folds before the largest app.
	if !ents["app/shop/Deployment/app-01"].Folded || ents["app/shop/Deployment/app-33"].Folded {
		t.Errorf("fold order wrong: app-01 folded=%v, app-33 folded=%v", ents["app/shop/Deployment/app-01"].Folded, ents["app/shop/Deployment/app-33"].Folded)
	}
}

func TestNoFoldingWithinLimits(t *testing.T) {
	m := model.New()
	apply(m, webApp(3)...)
	for id, e := range entities(m) {
		if e.Folded || e.Kind == "Counter" {
			t.Errorf("%s folded=%v kind=%s, want nothing folded and no counter", id, e.Folded, e.Kind)
		}
	}
}
