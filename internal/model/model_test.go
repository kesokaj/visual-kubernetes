package model_test

import (
	"maps"
	"slices"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	mt "github.com/kesokaj/visual-kubernetes/internal/model/modeltest"
)

// apply adds every object to m and returns the deltas of that one Apply.
func apply(m *model.Model, objs ...runtime.Object) []model.Delta {
	cs := make([]model.Change, len(objs))
	for i, o := range objs {
		cs[i] = model.Change{Obj: o}
	}
	return m.Apply(cs...)
}

func entities(m *model.Model) map[string]model.Entity {
	ents, _ := m.Snapshot()
	out := map[string]model.Entity{}
	for _, e := range ents {
		out[e.ID] = e
	}
	return out
}

func mustEntity(t *testing.T, m *model.Model, id string) model.Entity {
	t.Helper()
	e, ok := entities(m)[id]
	if !ok {
		t.Fatalf("entity %s missing; have %v", id, slices.Sorted(maps.Keys(entities(m))))
	}
	return e
}

// edgeIDs returns the ids of the edges of the given kinds (every kind when none is given).
func edgeIDs(m *model.Model, kinds ...model.EdgeKind) []string {
	_, edges := m.Snapshot()
	var out []string
	for _, e := range edges {
		if len(kinds) == 0 || slices.Contains(kinds, e.Kind) {
			out = append(out, e.ID)
		}
	}
	return out
}

func appsIn(m *model.Model) []string {
	var out []string
	for id := range entities(m) {
		if len(id) > 4 && id[:4] == "app/" {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

// webApp is a Deployment web in demo with one ReplicaSet and n running pods on node w1.
func webApp(n int, extra ...runtime.Object) []runtime.Object {
	d := mt.Deployment("demo", "web", int32(n), mt.Labels("app", "web"))
	rs := mt.ReplicaSet(d, "7c9d8")
	objs := []runtime.Object{mt.Node("w1", true), d, rs}
	for i := range n {
		objs = append(objs, mt.Running(mt.Scheduled(mt.PodOf(rs, string(rune('a'+i))), "w1"), true))
	}
	return append(objs, extra...)
}

func TestDeploymentServiceHPAIsOneApp(t *testing.T) {
	m := model.New()
	apply(m, webApp(2,
		mt.Service("demo", "web-svc", mt.Labels("app", "web")),
		mt.HPA("demo", "web", "Deployment", "web", 1, 5, 2),
	)...)

	if got := appsIn(m); !slices.Equal(got, []string{"app/demo/Deployment/web"}) {
		t.Fatalf("apps = %v, want exactly app/demo/Deployment/web", got)
	}
	a := mustEntity(t, m, "app/demo/Deployment/web")
	if a.Name != "web-svc" || a.Kind != "Deployment" || a.OwnerID != "apps/Deployment/demo/web" || a.Status != "2/2 ready" {
		t.Errorf("app = %+v; want named by its Service, kind Deployment, owner the Deployment, 2/2 ready", a)
	}
	if svc := mustEntity(t, m, "core/Service/demo/web-svc"); svc.AppID != a.ID {
		t.Errorf("Service appId = %q, want merged into %s", svc.AppID, a.ID)
	}
	if hpa := mustEntity(t, m, "autoscaling/HorizontalPodAutoscaler/demo/web"); hpa.AppID != a.ID || hpa.FilterGroup != model.ServicesConfig {
		t.Errorf("HPA = %+v, want satellite of %s in servicesConfig", hpa, a.ID)
	}
	if got := edgeIDs(m, model.EdgeSelects, model.EdgeRoutes, model.EdgeReferences); len(got) != 0 {
		t.Errorf("satellite edges = %v, want none (one app → appId, not edges)", got)
	}
	if pod := mustEntity(t, m, "core/Pod/demo/web-7c9d8-a"); pod.AppID != a.ID || pod.OwnerID != "apps/ReplicaSet/demo/web-7c9d8" || pod.NodeName != "w1" {
		t.Errorf("pod = %+v, want app %s, owner its ReplicaSet, node w1", pod, a.ID)
	}
	wantEdges := []string{"hosts:core/Node/_/w1->app/demo/Deployment/web", "member:cp/apiserver->core/Node/_/w1"}
	if got := edgeIDs(m); !slices.Equal(got, wantEdges) {
		t.Errorf("edges = %v, want %v", got, wantEdges)
	}
}

func TestServiceOverTwoWorkloadsStaysSeparate(t *testing.T) {
	m := model.New()
	apply(m,
		mt.Deployment("demo", "api", 1, mt.Labels("app", "api", "tier", "backend")),
		mt.Deployment("demo", "worker", 1, mt.Labels("app", "worker", "tier", "backend")),
		mt.Service("demo", "backend", mt.Labels("tier", "backend")),
	)
	svc := mustEntity(t, m, "core/Service/demo/backend")
	if svc.AppID != "" || svc.FilterGroup != model.ServicesConfig {
		t.Errorf("Service = %+v, want its own servicesConfig entity", svc)
	}
	want := []string{
		"selects:core/Service/demo/backend->app/demo/Deployment/api",
		"selects:core/Service/demo/backend->app/demo/Deployment/worker",
	}
	if got := edgeIDs(m, model.EdgeSelects); !slices.Equal(got, want) {
		t.Errorf("selects edges = %v, want %v", got, want)
	}
	if a := mustEntity(t, m, "app/demo/Deployment/api"); a.Name != "api" || a.Status != "0/1 ready" {
		t.Errorf("app api = %+v, want its own name and 0/1 ready before any pod", a)
	}
}

func TestBarePodIsItsOwnApp(t *testing.T) {
	m := model.New()
	apply(m, mt.Pod("demo", "debug", nil, nil))
	a := mustEntity(t, m, "app/demo/Pod/debug")
	if a.Kind != "Pod" || a.OwnerID != "" || a.FilterGroup != model.Workloads || a.Status != "0/1 ready" {
		t.Errorf("app = %+v, want kind Pod, no owner, workloads, 0/1 ready", a)
	}
	if p := mustEntity(t, m, "core/Pod/demo/debug"); p.AppID != a.ID || p.Status != "Pending" {
		t.Errorf("pod = %+v, want app %s and Pending", p, a.ID)
	}
}

// TestBarePodBesideSameNamedDeployment: `kubectl run web` next to Deployment web is a second
// app, never counted in the Deployment's bubble (D15; app ids carry the kind, D23).
func TestBarePodBesideSameNamedDeployment(t *testing.T) {
	m := model.New()
	apply(m, webApp(1, mt.Pod("demo", "web", mt.Labels("run", "web"), nil))...)
	want := []string{"app/demo/Deployment/web", "app/demo/Pod/web"}
	if got := appsIn(m); !slices.Equal(got, want) {
		t.Fatalf("apps = %v, want %v", got, want)
	}
	if s := mustEntity(t, m, "app/demo/Deployment/web").Status; s != "1/1 ready" {
		t.Errorf("deployment status = %q, want 1/1 ready (the bare pod is not its replica)", s)
	}
	if got := mustEntity(t, m, "core/Pod/demo/web").AppID; got != "app/demo/Pod/web" {
		t.Errorf("bare pod appId = %q, want app/demo/Pod/web", got)
	}
}

func TestStaticPodOwnedByNodeIsItsOwnApp(t *testing.T) {
	m := model.New()
	p := mt.Pod("kube-system", "kube-apiserver-cp1", mt.Labels("component", "kube-apiserver"), mt.ControllerRef("v1", "Node", "cp1"))
	apply(m, mt.Node("cp1", true), p)
	a := mustEntity(t, m, "app/kube-system/Pod/kube-apiserver-cp1")
	if a.FilterGroup != model.System {
		t.Errorf("app group = %s, want system (kube-system)", a.FilterGroup)
	}
	if got := mustEntity(t, m, "core/Pod/kube-system/kube-apiserver-cp1").OwnerID; got != "core/Node/_/cp1" {
		t.Errorf("ownerId = %q, want the cluster-scoped node id", got)
	}
}

func TestCronJobChainGroupsUnderTopOwner(t *testing.T) {
	m := model.New()
	cj := &batchv1.CronJob{ObjectMeta: metav1.ObjectMeta{Namespace: "ops", Name: "backup"}}
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{
		Namespace: "ops", Name: "backup-123",
		OwnerReferences: []metav1.OwnerReference{*mt.ControllerRef("batch/v1", "CronJob", "backup")},
	}}
	pod := mt.Pod("ops", "backup-123-x", nil, mt.ControllerRef("batch/v1", "Job", "backup-123"))
	apply(m, cj, job, pod)
	if got := appsIn(m); !slices.Equal(got, []string{"app/ops/CronJob/backup"}) {
		t.Fatalf("apps = %v, want only app/ops/CronJob/backup", got)
	}
	if a := mustEntity(t, m, "app/ops/CronJob/backup"); a.Kind != "CronJob" || a.Group != "batch" {
		t.Errorf("app = %+v, want the CronJob", a)
	}
}

func TestNodeAndPodStatus(t *testing.T) {
	m := model.New()
	cordoned := mt.Node("w2", false)
	cordoned.Spec.Unschedulable = true
	crash := mt.Running(mt.Pod("demo", "crash", nil, nil), false)
	crash.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "main", State: corev1.ContainerState{
		Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"},
	}}}
	apply(m, cordoned, crash, mt.Unschedulable(mt.Pod("demo", "big", nil, nil)), mt.Running(mt.Pod("demo", "warm", nil, nil), false))
	for id, want := range map[string]string{
		"core/Node/_/w2":      "NotReady,SchedulingDisabled",
		"core/Pod/demo/crash": "CrashLoopBackOff",
		"core/Pod/demo/big":   "Unschedulable",
		"core/Pod/demo/warm":  "NotReady",
		model.APIServerID:     "",
	} {
		if got := mustEntity(t, m, id).Status; got != want {
			t.Errorf("%s status = %q, want %q", id, got, want)
		}
	}
}

func TestFilterGroups(t *testing.T) {
	m := model.New()
	apply(m, webApp(1, mt.Ingress("demo", "web", "web"), mt.Service("demo", "web", mt.Labels("app", "web")))...)
	apply(m, mt.DaemonSet("kube-system", "kube-proxy", mt.Labels("k8s-app", "kube-proxy"), 1))
	for id, want := range map[string]model.FilterGroup{
		model.APIServerID:                      model.ControlPlane,
		"core/Node/_/w1":                       model.Nodes,
		"app/demo/Deployment/web":              model.Workloads,
		"core/Pod/demo/web-7c9d8-a":            model.Pods,
		"core/Service/demo/web":                model.ServicesConfig,
		"networking.k8s.io/Ingress/demo/web":   model.Ingress,
		"app/kube-system/DaemonSet/kube-proxy": model.System,
	} {
		if got := mustEntity(t, m, id).FilterGroup; got != want {
			t.Errorf("%s filterGroup = %s, want %s", id, got, want)
		}
	}
}
