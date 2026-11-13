package demo

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	"github.com/kesokaj/visual-kubernetes/internal/stream"
)

func doScaleGet(h http.Handler, host, id string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/demo/workloads/scale?id="+id, nil)
	req.Host = host
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func doScalePatch(h http.Handler, host, origin, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPatch, "/api/demo/workloads/scale", strings.NewReader(body))
	req.Host = host
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestWorkloadScaleDeploymentAndStatefulSetWithManagedByHPA(t *testing.T) {
	two := int32(2)
	minOne := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "web", ResourceVersion: "18"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &two,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "web", Image: "nginx"}}},
			},
		},
	}
	// StatefulSet with nil Spec.Replicas defaults to 1.
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "db", ResourceVersion: "7"},
		Spec: appsv1.StatefulSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "db"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "db"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "db", Image: "postgres"}}},
			},
		},
	}
	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "web-hpa", ResourceVersion: "4"},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				APIVersion: "apps/v1", Kind: "Deployment", Name: "web",
			},
			MinReplicas: &minOne,
			MaxReplicas: 10,
		},
	}

	m := model.New()
	m.Apply(
		model.Change{Obj: dep.DeepCopy()},
		model.Change{Obj: sts.DeepCopy()},
		model.Change{Obj: hpa.DeepCopy()},
	)

	cs := fake.NewClientset(dep, sts, hpa)
	rv := 30
	cs.PrependReactor("update", "*", func(act k8stesting.Action) (bool, runtime.Object, error) {
		upd := act.(k8stesting.UpdateAction).GetObject().DeepCopyObject()
		rv++
		if metaObj, ok := upd.(metav1.Object); ok {
			metaObj.SetResourceVersion(fmt.Sprintf("%d", rv))
			_ = cs.Tracker().Update(act.GetResource(), upd, metaObj.GetNamespace())
		}
		return true, upd, nil
	})
	h := NewServer(Options{
		Catalog:   &ModelCatalog{Model: m, InCluster: true},
		Workloads: &KubeWorkloads{Client: cs, Model: m},
	}).Handler()

	// 1. GET Deployment with HPA.
	getDep := doScaleGet(h, "localhost:8080", "apps/Deployment/shop/web")
	if getDep.Code != http.StatusOK {
		t.Fatalf("GET Deployment scale = %d %q, want 200", getDep.Code, getDep.Body.String())
	}
	var depResp WorkloadScaleResponse
	if err := json.Unmarshal(getDep.Body.Bytes(), &depResp); err != nil {
		t.Fatal(err)
	}
	if depResp.Kind != "Deployment" || depResp.Replicas != 2 || depResp.MinReplicas != 0 || depResp.MaxReplicas != 50 || depResp.ManagedByHPA != "web-hpa" || depResp.ResourceVersion != "18" {
		t.Fatalf("GET Deployment response = %+v", depResp)
	}

	// 2. PATCH Deployment replicas -> 0 and -> 50 (boundary bounds).
	for _, target := range []struct {
		inRV string
		want int32
	}{
		{inRV: "18", want: 0},
		{inRV: "31", want: 50},
	} {
		rec := doScalePatch(h, "localhost:8080", "http://localhost:8080",
			fmt.Sprintf(`{"id":"apps/Deployment/shop/web","resourceVersion":%q,"replicas":%d}`, target.inRV, target.want))
		if rec.Code != http.StatusOK {
			t.Fatalf("PATCH Deployment -> %d = %d %q", target.want, rec.Code, rec.Body.String())
		}
		var out WorkloadScaleResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if out.Replicas != target.want || out.ManagedByHPA != "web-hpa" {
			t.Fatalf("PATCH Deployment response = %+v, want %d with web-hpa", out, target.want)
		}
	}

	// 3. GET StatefulSet with nil Spec.Replicas reads as 1 and unmanaged (""), then PATCH -> 3.
	getSts := doScaleGet(h, "localhost:8080", "apps/StatefulSet/shop/db")
	if getSts.Code != http.StatusOK {
		t.Fatalf("GET StatefulSet scale = %d %q", getSts.Code, getSts.Body.String())
	}
	var stsResp WorkloadScaleResponse
	_ = json.Unmarshal(getSts.Body.Bytes(), &stsResp)
	if stsResp.Kind != "StatefulSet" || stsResp.Replicas != 1 || stsResp.ManagedByHPA != "" {
		t.Fatalf("GET StatefulSet response = %+v, want replicas=1 unmanaged", stsResp)
	}
	patchSts := doScalePatch(h, "localhost:8080", "http://localhost:8080",
		`{"id":"apps/StatefulSet/shop/db","resourceVersion":"7","replicas":3}`)
	if patchSts.Code != http.StatusOK {
		t.Fatalf("PATCH StatefulSet -> 3 = %d %q", patchSts.Code, patchSts.Body.String())
	}
	storedSts, _ := cs.AppsV1().StatefulSets("shop").Get(t.Context(), "db", metav1.GetOptions{})
	if storedSts.Spec.Replicas == nil || *storedSts.Spec.Replicas != 3 {
		t.Errorf("stored StatefulSet replicas = %v, want 3", storedSts.Spec.Replicas)
	}

	for _, act := range cs.Actions() {
		if act.GetResource().Resource == "secrets" {
			t.Errorf("unexpected Secret action: %+v", act)
		}
		if u, ok := act.(k8stesting.UpdateActionImpl); ok && u.GetUpdateOptions().FieldManager != FieldManagerName {
			t.Errorf("FieldManager = %q, want %q", u.GetUpdateOptions().FieldManager, FieldManagerName)
		}
	}
}

func TestWorkloadScaleGuardsValidationAndConflict(t *testing.T) {
	one := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "web", ResourceVersion: "12"},
		Spec:       appsv1.DeploymentSpec{Replicas: &one},
	}
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "agent", ResourceVersion: "5"},
	}
	cs := fake.NewClientset(dep, ds)
	h := NewServer(Options{
		Catalog:   &ModelCatalog{Model: model.New()},
		Workloads: &KubeWorkloads{Client: cs},
	}).Handler()

	// Protected namespaces -> 403 with zero client actions.
	for _, id := range []string{
		"apps/Deployment/kube-system/coredns",
		"apps/DaemonSet/kube-system/kindnet",
		"apps/StatefulSet/visual-kubernetes/vk",
		"apps/Deployment/gke-managed-cim/agent",
		"apps/Deployment/gmp-system/collector",
	} {
		if rec := doScaleGet(h, "localhost:8080", id); rec.Code != http.StatusForbidden {
			t.Errorf("GET %s = %d, want 403", id, rec.Code)
		}
		if rec := doScalePatch(h, "localhost:8080", "http://localhost:8080",
			fmt.Sprintf(`{"id":%q,"resourceVersion":"1","replicas":2}`, id)); rec.Code != http.StatusForbidden {
			t.Errorf("PATCH %s = %d, want 403", id, rec.Code)
		}
	}
	if len(cs.Actions()) != 0 {
		t.Fatalf("protected scale requests hit client: %+v", cs.Actions())
	}

	// SameOrigin & LoopbackOrIAP guards.
	if rec := doScalePatch(h, "localhost:8080", "", `{"id":"apps/Deployment/shop/web","resourceVersion":"12","replicas":2}`); rec.Code != http.StatusForbidden {
		t.Errorf("missing Origin = %d, want 403", rec.Code)
	}
	if rec := doScaleGet(h, "evil.example.com:8080", "apps/Deployment/shop/web"); rec.Code != http.StatusForbidden {
		t.Errorf("non-loopback Host = %d, want 403", rec.Code)
	}

	// DaemonSet, dot-segments, and invalid kinds / bounds -> 400.
	for _, badID := range []string{
		"apps/DaemonSet/shop/agent",
		"apps/Deployment/../web",
		"apps/Deployment/shop/..",
	} {
		if rec := doScaleGet(h, "localhost:8080", badID); rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d %q, want 400", badID, rec.Code, rec.Body.String())
		}
	}
	for _, body := range []string{
		`{"id":"apps/DaemonSet/shop/agent","resourceVersion":"5","replicas":2}`,
		`{"id":"apps/ReplicaSet/shop/web-abc","resourceVersion":"12","replicas":2}`,
		`{"id":"core/Pod/shop/web-0","resourceVersion":"12","replicas":2}`,
		`{"id":"apps/Deployment/shop/web","resourceVersion":"12","replicas":-1}`,
		`{"id":"apps/Deployment/shop/web","resourceVersion":"12","replicas":51}`,
		`{"id":"apps/Deployment/shop/web","resourceVersion":"12"}`,
		`{"id":"apps/Deployment/shop/web","resourceVersion":"","replicas":2}`,
		`{"id":"apps/Deployment/shop/web","resourceVersion":"12","replicas":2,"extra":true}`,
		`{"id":"apps/Deployment/shop/web","resourceVersion":"12","replicas":2}{}`,
	} {
		if rec := doScalePatch(h, "localhost:8080", "http://localhost:8080", body); rec.Code != http.StatusBadRequest {
			t.Errorf("PATCH bad payload %s = %d %q, want 400", body, rec.Code, rec.Body.String())
		}
	}

	// Missing workload -> 404.
	if rec := doScaleGet(h, "localhost:8080", "apps/Deployment/shop/missing"); rec.Code != http.StatusNotFound {
		t.Errorf("GET missing = %d, want 404", rec.Code)
	}

	// Stale rv and API conflict -> 409.
	if rec := doScalePatch(h, "localhost:8080", "http://localhost:8080",
		`{"id":"apps/Deployment/shop/web","resourceVersion":"11","replicas":3}`); rec.Code != http.StatusConflict {
		t.Errorf("stale rv = %d, want 409", rec.Code)
	}
	cs.PrependReactor("update", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewConflict(schema.GroupResource{Group: "apps", Resource: "deployments"}, "web", errors.New("conflict"))
	})
	if rec := doScalePatch(h, "localhost:8080", "http://localhost:8080",
		`{"id":"apps/Deployment/shop/web","resourceVersion":"12","replicas":3}`); rec.Code != http.StatusConflict {
		t.Errorf("API conflict = %d, want 409", rec.Code)
	}
}

func TestWorkloadScaleReplayDeltasAndZeroSeed(t *testing.T) {
	hub := stream.NewHub(stream.HubOptions{})
	_ = hub.Publish(model.Event{
		Type: stream.EventSnapshot,
		Data: model.SnapshotData{
			Entities: []model.Entity{
				{ID: "app/shop/Deployment/web", Group: "apps", Kind: "Deployment", Namespace: "shop", Name: "web", Status: "2/2 ready", OwnerID: "apps/Deployment/shop/web"},
				{ID: "app/shop/Deployment/idle", Group: "apps", Kind: "Deployment", Namespace: "shop", Name: "idle", Status: "0/0 ready", OwnerID: "apps/Deployment/shop/idle"},
				{ID: "autoscaling/HorizontalPodAutoscaler/shop/web", Group: "autoscaling", Kind: "HorizontalPodAutoscaler", Namespace: "shop", Name: "web", AppID: "app/shop/Deployment/web", Status: "2 replicas (min 1, max 10)"},
			},
		},
	})
	h := NewReplayServer(hub, false).Handler()

	// Seeded from "0/0 ready" -> 0 replicas, unmanaged.
	idleRec := doScaleGet(h, "127.0.0.1:18090", "apps/Deployment/shop/idle")
	if idleRec.Code != http.StatusOK {
		t.Fatalf("replay GET idle = %d %q", idleRec.Code, idleRec.Body.String())
	}
	var idleResp WorkloadScaleResponse
	_ = json.Unmarshal(idleRec.Body.Bytes(), &idleResp)
	if idleResp.Replicas != 0 || idleResp.ManagedByHPA != "" {
		t.Fatalf("replay idle = %+v, want replicas=0 unmanaged", idleResp)
	}

	// Seeded from "2/2 ready" + HPA -> replicas=2, managedByHPA="web".
	webRec := doScaleGet(h, "127.0.0.1:18090", "apps/Deployment/shop/web")
	var webResp WorkloadScaleResponse
	_ = json.Unmarshal(webRec.Body.Bytes(), &webResp)
	if webResp.Replicas != 2 || webResp.ManagedByHPA != "web" {
		t.Fatalf("replay web = %+v, want replicas=2 managedByHPA=web", webResp)
	}

	// Patch web -> 4 updates Hub app Status to "2/4 ready".
	patchRec := doScalePatch(h, "127.0.0.1:18090", "http://127.0.0.1:18090",
		`{"id":"apps/Deployment/shop/web","resourceVersion":"1","replicas":4}`)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("replay PATCH web -> 4 = %d %q", patchRec.Code, patchRec.Body.String())
	}
	ent, ok := hub.Entity("app/shop/Deployment/web")
	if !ok || ent.Status != "2/4 ready" {
		t.Errorf("hub app entity after scale = %+v, want Status 2/4 ready", ent)
	}

	// Stale rv -> 409.
	if stale := doScalePatch(h, "127.0.0.1:18090", "http://127.0.0.1:18090",
		`{"id":"apps/Deployment/shop/web","resourceVersion":"1","replicas":1}`); stale.Code != http.StatusConflict {
		t.Errorf("replay stale rv = %d, want 409", stale.Code)
	}
}
