package demo

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/kesokaj/visual-kubernetes/internal/model"
	"github.com/kesokaj/visual-kubernetes/internal/stream"
)

func doAutoGet(h http.Handler, host, id string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/demo/autoscalers?id="+id, nil)
	req.Host = host
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func doAutoPatch(h http.Handler, host, origin, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPatch, "/api/demo/autoscalers", strings.NewReader(body))
	req.Host = host
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func newFakeVPADynClient(objs ...runtime.Object) *dynfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	gvrToListKind := map[schema.GroupVersionResource]string{
		{Group: "autoscaling.k8s.io", Version: "v1", Resource: "verticalpodautoscalers"}: "VerticalPodAutoscalerList",
	}
	return dynfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKind, objs...)
}

func TestAutoscalerHPAGetPatchAndValidation(t *testing.T) {
	// HPA with nil Spec.MinReplicas reads as 1.
	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "web", ResourceVersion: "9"},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{APIVersion: "apps/v1", Kind: "Deployment", Name: "web"},
			MaxReplicas:    10,
		},
		Status: autoscalingv2.HorizontalPodAutoscalerStatus{CurrentReplicas: 2},
	}
	cs := fake.NewClientset(hpa)
	cs.PrependReactor("update", "horizontalpodautoscalers", func(act k8stesting.Action) (bool, runtime.Object, error) {
		upd := act.(k8stesting.UpdateAction).GetObject().(*autoscalingv2.HorizontalPodAutoscaler).DeepCopy()
		upd.ResourceVersion = "10"
		_ = cs.Tracker().Update(act.GetResource(), upd, upd.Namespace)
		return true, upd, nil
	})
	h := NewServer(Options{
		Catalog:     &ModelCatalog{Model: model.New()},
		Autoscalers: &KubeAutoscalers{Client: cs, Dyn: newFakeVPADynClient()},
	}).Handler()

	// 1. GET HPA with nil MinReplicas -> minReplicas=1, maxReplicas=10, currentReplicas=2.
	getRec := doAutoGet(h, "localhost:8080", "autoscaling/HorizontalPodAutoscaler/shop/web")
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET HPA = %d %q, want 200", getRec.Code, getRec.Body.String())
	}
	var got AutoscalerResponse
	if err := json.Unmarshal(getRec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Kind != "HorizontalPodAutoscaler" || got.HPA == nil || got.VPA != nil ||
		got.HPA.MinReplicas != 1 || got.HPA.MaxReplicas != 10 || got.HPA.CurrentReplicas != 2 {
		t.Fatalf("GET HPA payload = %+v", got)
	}

	// 2. PATCH HPA minReplicas=3, maxReplicas=15 -> 200 with FieldManager.
	patchRec := doAutoPatch(h, "localhost:8080", "http://localhost:8080",
		`{"id":"autoscaling/HorizontalPodAutoscaler/shop/web","resourceVersion":"9","hpa":{"minReplicas":3,"maxReplicas":15}}`)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("PATCH HPA = %d %q, want 200", patchRec.Code, patchRec.Body.String())
	}
	var patched AutoscalerResponse
	_ = json.Unmarshal(patchRec.Body.Bytes(), &patched)
	if patched.ResourceVersion != "10" || patched.HPA == nil || patched.HPA.MinReplicas != 3 || patched.HPA.MaxReplicas != 15 {
		t.Fatalf("PATCH HPA payload = %+v", patched)
	}

	// 3. Invalid HPA bounds and cross-kind payloads -> 400.
	for _, body := range []string{
		`{"id":"autoscaling/HorizontalPodAutoscaler/shop/web","resourceVersion":"10","hpa":{"minReplicas":0,"maxReplicas":10}}`,
		`{"id":"autoscaling/HorizontalPodAutoscaler/shop/web","resourceVersion":"10","hpa":{"minReplicas":5,"maxReplicas":4}}`,
		`{"id":"autoscaling/HorizontalPodAutoscaler/shop/web","resourceVersion":"10","hpa":{"minReplicas":1,"maxReplicas":51}}`,
		`{"id":"autoscaling/HorizontalPodAutoscaler/shop/web","resourceVersion":"10","hpa":{"minReplicas":1}}`,
		`{"id":"autoscaling/HorizontalPodAutoscaler/shop/web","resourceVersion":"10","vpa":{"updateMode":"Auto","containers":[]}}`,
	} {
		if rec := doAutoPatch(h, "localhost:8080", "http://localhost:8080", body); rec.Code != http.StatusBadRequest {
			t.Errorf("PATCH invalid HPA %s = %d %q, want 400", body, rec.Code, rec.Body.String())
		}
	}

	// 4. Stale rv & API conflict -> 409.
	if rec := doAutoPatch(h, "localhost:8080", "http://localhost:8080",
		`{"id":"autoscaling/HorizontalPodAutoscaler/shop/web","resourceVersion":"9","hpa":{"minReplicas":2,"maxReplicas":8}}`); rec.Code != http.StatusConflict {
		t.Errorf("stale HPA rv = %d, want 409", rec.Code)
	}
	cs.PrependReactor("update", "horizontalpodautoscalers", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewConflict(schema.GroupResource{Group: "autoscaling", Resource: "horizontalpodautoscalers"}, "web", errors.New("conflict"))
	})
	if rec := doAutoPatch(h, "localhost:8080", "http://localhost:8080",
		`{"id":"autoscaling/HorizontalPodAutoscaler/shop/web","resourceVersion":"10","hpa":{"minReplicas":2,"maxReplicas":8}}`); rec.Code != http.StatusConflict {
		t.Errorf("API HPA conflict = %d, want 409", rec.Code)
	}
}

func TestAutoscalerVPAUnstructuredEdgeCasesAndMissingCRD(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "worker"},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "worker-bin", Image: "busybox"}}},
			},
		},
	}
	m := model.New()
	m.Apply(model.Change{Obj: dep})

	// VPA 1: nil updatePolicy & nil resourcePolicy -> defaults updateMode="Auto", seeds containerName="worker-bin" from Model.
	bareVPA := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "autoscaling.k8s.io/v1",
		"kind":       "VerticalPodAutoscaler",
		"metadata":   map[string]any{"namespace": "shop", "name": "bare", "resourceVersion": "3"},
		"spec": map[string]any{
			"targetRef": map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "worker"},
		},
	}}
	// VPA 2: orphan target -> seeds containerName="*".
	orphanVPA := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "autoscaling.k8s.io/v1",
		"kind":       "VerticalPodAutoscaler",
		"metadata":   map[string]any{"namespace": "shop", "name": "orphan", "resourceVersion": "4"},
		"spec": map[string]any{
			"targetRef": map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "unknown"},
		},
	}}
	// VPA 3: wildcard "*" policy with minAllowed, controlledResources, controlledValues, and numeric int64 CPU quantity.
	richVPA := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "autoscaling.k8s.io/v1",
		"kind":       "VerticalPodAutoscaler",
		"metadata":   map[string]any{"namespace": "shop", "name": "rich", "resourceVersion": "5"},
		"spec": map[string]any{
			"targetRef":    map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "worker"},
			"updatePolicy": map[string]any{"updateMode": "InPlaceOrRecreate", "minReplicas": int64(2)},
			"resourcePolicy": map[string]any{
				"containerPolicies": []any{
					map[string]any{
						"containerName":       "*",
						"controlledValues":    "RequestsAndLimits",
						"controlledResources": []any{"cpu", "memory"},
						"minAllowed":          map[string]any{"cpu": "50m", "memory": "64Mi"},
						"maxAllowed":          map[string]any{"cpu": int64(2), "memory": "512Mi"},
					},
				},
			},
		},
	}}

	dyn := newFakeVPADynClient(bareVPA, orphanVPA, richVPA)
	rv := 20
	dyn.PrependReactor("update", "verticalpodautoscalers", func(act k8stesting.Action) (bool, runtime.Object, error) {
		upd := act.(k8stesting.UpdateAction).GetObject().(*unstructured.Unstructured).DeepCopy()
		rv++
		upd.SetResourceVersion(fmt.Sprintf("%d", rv))
		_ = dyn.Tracker().Update(act.GetResource(), upd, upd.GetNamespace())
		return true, upd, nil
	})
	h := NewServer(Options{
		Catalog:     &ModelCatalog{Model: m},
		Autoscalers: &KubeAutoscalers{Client: fake.NewClientset(), Dyn: dyn, Model: m},
	}).Handler()

	// 1. GET bareVPA -> updateMode="Auto", allowedModes has InPlaceOrRecreate, seeded container="worker-bin".
	bareRec := doAutoGet(h, "localhost:8080", "autoscaling.k8s.io/VerticalPodAutoscaler/shop/bare")
	if bareRec.Code != http.StatusOK {
		t.Fatalf("GET bare VPA = %d %q", bareRec.Code, bareRec.Body.String())
	}
	var bareGot AutoscalerResponse
	_ = json.Unmarshal(bareRec.Body.Bytes(), &bareGot)
	wantModes := []string{"Off", "Initial", "Recreate", "Auto", "InPlaceOrRecreate"}
	if bareGot.VPA == nil || bareGot.VPA.UpdateMode != "Auto" || !slices.Equal(bareGot.VPA.AllowedModes, wantModes) ||
		len(bareGot.VPA.Containers) != 1 || bareGot.VPA.Containers[0].ContainerName != "worker-bin" {
		t.Fatalf("GET bare VPA = %+v", bareGot.VPA)
	}

	// 2. GET orphanVPA -> seeded container="*".
	orphanRec := doAutoGet(h, "localhost:8080", "autoscaling.k8s.io/VerticalPodAutoscaler/shop/orphan")
	var orphanGot AutoscalerResponse
	_ = json.Unmarshal(orphanRec.Body.Bytes(), &orphanGot)
	if orphanGot.VPA == nil || len(orphanGot.VPA.Containers) != 1 || orphanGot.VPA.Containers[0].ContainerName != "*" {
		t.Fatalf("GET orphan VPA = %+v, want wildcard *", orphanGot.VPA)
	}

	// 2b. Patching bareVPA's updateMode alone (either with containers: [] or with the seeded blank row)
	// must NOT fabricate spec.resourcePolicy or an invalid empty containerPolicies: [] slice.
	if rec := doAutoPatch(h, "localhost:8080", "http://localhost:8080",
		`{"id":"autoscaling.k8s.io/VerticalPodAutoscaler/shop/bare","resourceVersion":"3","vpa":{"updateMode":"Recreate","containers":[]}}`); rec.Code != http.StatusOK {
		t.Fatalf("PATCH bare VPA empty containers = %d %q", rec.Code, rec.Body.String())
	}
	vpaGVRCheck := schema.GroupVersionResource{Group: "autoscaling.k8s.io", Version: "v1", Resource: "verticalpodautoscalers"}
	storedBare1, _ := dyn.Resource(vpaGVRCheck).Namespace("shop").Get(t.Context(), "bare", metav1.GetOptions{})
	if _, hasResPol := storedBare1.Object["spec"].(map[string]any)["resourcePolicy"]; hasResPol {
		t.Errorf("PATCH bare VPA with containers: [] fabricated resourcePolicy: %+v", storedBare1.Object["spec"])
	}
	if rec := doAutoPatch(h, "localhost:8080", "http://localhost:8080",
		fmt.Sprintf(`{"id":"autoscaling.k8s.io/VerticalPodAutoscaler/shop/bare","resourceVersion":%q,"vpa":{"updateMode":"Initial","containers":[{"containerName":"worker-bin","maxCPU":"","maxMemory":""}]}}`, storedBare1.GetResourceVersion())); rec.Code != http.StatusOK {
		t.Fatalf("PATCH bare VPA blank seeded row = %d %q", rec.Code, rec.Body.String())
	}
	storedBare2, _ := dyn.Resource(vpaGVRCheck).Namespace("shop").Get(t.Context(), "bare", metav1.GetOptions{})
	if _, hasResPol := storedBare2.Object["spec"].(map[string]any)["resourcePolicy"]; hasResPol {
		t.Errorf("PATCH bare VPA with blank seeded row fabricated resourcePolicy: %+v", storedBare2.Object["spec"])
	}

	// 3. GET richVPA -> int64 cpu formatted as "2", memory "512Mi"; PATCH clearing memory ("") and setting cpu="1500m" preserves minAllowed, controlledValues, controlledResources, and updatePolicy.minReplicas.
	richRec := doAutoGet(h, "localhost:8080", "autoscaling.k8s.io/VerticalPodAutoscaler/shop/rich")
	var richGot AutoscalerResponse
	_ = json.Unmarshal(richRec.Body.Bytes(), &richGot)
	if richGot.VPA == nil || richGot.VPA.UpdateMode != "InPlaceOrRecreate" || richGot.VPA.Containers[0].MaxCPU != "2" || richGot.VPA.Containers[0].MaxMemory != "512Mi" {
		t.Fatalf("GET rich VPA = %+v", richGot.VPA)
	}

	patchRich := doAutoPatch(h, "localhost:8080", "http://localhost:8080",
		`{"id":"autoscaling.k8s.io/VerticalPodAutoscaler/shop/rich","resourceVersion":"5","vpa":{"updateMode":"Recreate","containers":[{"containerName":"*","maxCPU":"1500m","maxMemory":""}]}}`)
	if patchRich.Code != http.StatusOK {
		t.Fatalf("PATCH rich VPA = %d %q", patchRich.Code, patchRich.Body.String())
	}
	vpaGVR := schema.GroupVersionResource{Group: "autoscaling.k8s.io", Version: "v1", Resource: "verticalpodautoscalers"}
	storedRich, err := dyn.Resource(vpaGVR).Namespace("shop").Get(t.Context(), "rich", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	pols, _, _ := unstructured.NestedSlice(storedRich.Object, "spec", "resourcePolicy", "containerPolicies")
	pol0 := pols[0].(map[string]any)
	maxAllowed, hasMax := pol0["maxAllowed"].(map[string]any)
	if !hasMax || maxAllowed["cpu"] != "1500m" {
		t.Fatalf("maxAllowed after clearing memory = %+v", maxAllowed)
	}
	if _, memPresent := maxAllowed["memory"]; memPresent {
		t.Errorf("empty maxMemory was not deleted from maxAllowed: %+v", maxAllowed)
	}
	minAllowed, _ := pol0["minAllowed"].(map[string]any)
	if minAllowed["cpu"] != "50m" || minAllowed["memory"] != "64Mi" || pol0["controlledValues"] != "RequestsAndLimits" {
		t.Errorf("VPA patch clobbered preserved policy fields: %+v", pol0)
	}
	if minRep, _, _ := unstructured.NestedInt64(storedRich.Object, "spec", "updatePolicy", "minReplicas"); minRep != 2 {
		t.Errorf("VPA patch clobbered updatePolicy.minReplicas = %d, want 2", minRep)
	}

	// Clearing BOTH cpu and memory deletes maxAllowed entirely while keeping minAllowed.
	patchClearAll := doAutoPatch(h, "localhost:8080", "http://localhost:8080",
		fmt.Sprintf(`{"id":"autoscaling.k8s.io/VerticalPodAutoscaler/shop/rich","resourceVersion":%q,"vpa":{"updateMode":"Off","containers":[{"containerName":"*","maxCPU":"","maxMemory":""}]}}`, storedRich.GetResourceVersion()))
	if patchClearAll.Code != http.StatusOK {
		t.Fatalf("PATCH clear all maxAllowed = %d %q", patchClearAll.Code, patchClearAll.Body.String())
	}
	storedCleared, _ := dyn.Resource(vpaGVR).Namespace("shop").Get(t.Context(), "rich", metav1.GetOptions{})
	pols2, _, _ := unstructured.NestedSlice(storedCleared.Object, "spec", "resourcePolicy", "containerPolicies")
	if _, stillHasMax := pols2[0].(map[string]any)["maxAllowed"]; stillHasMax {
		t.Errorf("empty maxAllowed map was not removed: %+v", pols2[0])
	}

	// 4. Validation: protected ns 403, path-traversal / invalid DNS segments 400, invalid quantities / limits / modes / >32 accumulation -> 400.
	if rec := doAutoGet(h, "localhost:8080", "autoscaling.k8s.io/VerticalPodAutoscaler/kube-system/sys"); rec.Code != http.StatusForbidden {
		t.Errorf("protected VPA = %d, want 403", rec.Code)
	}
	for _, badID := range []string{
		"autoscaling.k8s.io/VerticalPodAutoscaler/../bare",
		"autoscaling.k8s.io/VerticalPodAutoscaler/./bare",
		"autoscaling.k8s.io/VerticalPodAutoscaler/shop/..",
		"autoscaling.k8s.io/VerticalPodAutoscaler/shop/.",
		"autoscaling/HorizontalPodAutoscaler/../web",
		"autoscaling/HorizontalPodAutoscaler/shop/..",
	} {
		if rec := doAutoGet(h, "localhost:8080", badID); rec.Code != http.StatusBadRequest {
			t.Errorf("GET dot-segment %s = %d %q, want 400", badID, rec.Code, rec.Body.String())
		}
	}
	for _, body := range []string{
		`{"id":"autoscaling.k8s.io/VerticalPodAutoscaler/shop/bare","resourceVersion":"3","vpa":{"updateMode":"Bogus","containers":[{"containerName":"worker-bin","maxCPU":"1","maxMemory":"1Gi"}]}}`,
		`{"id":"autoscaling.k8s.io/VerticalPodAutoscaler/shop/bare","resourceVersion":"3","vpa":{"updateMode":"Auto","containers":[{"containerName":"worker-bin","maxCPU":"65","maxMemory":"1Gi"}]}}`,
		`{"id":"autoscaling.k8s.io/VerticalPodAutoscaler/shop/bare","resourceVersion":"3","vpa":{"updateMode":"Auto","containers":[{"containerName":"worker-bin","maxCPU":"0","maxMemory":"1Gi"}]}}`,
		`{"id":"autoscaling.k8s.io/VerticalPodAutoscaler/shop/bare","resourceVersion":"3","vpa":{"updateMode":"Auto","containers":[{"containerName":"worker-bin","maxCPU":"not-a-qty","maxMemory":"1Gi"}]}}`,
		`{"id":"autoscaling.k8s.io/VerticalPodAutoscaler/shop/bare","resourceVersion":"3","vpa":{"updateMode":"Auto","containers":[{"containerName":"worker-bin","maxCPU":"1","maxMemory":"129Gi"}]}}`,
		`{"id":"autoscaling.k8s.io/VerticalPodAutoscaler/shop/bare","resourceVersion":"3","vpa":{"updateMode":"Auto","containers":[{"containerName":"","maxCPU":"1","maxMemory":"1Gi"}]}}`,
		`{"id":"autoscaling.k8s.io/VerticalPodAutoscaler/shop/bare","resourceVersion":"3","vpa":{"updateMode":"Auto","containers":[{"containerName":"bad/name","maxCPU":"1","maxMemory":"1Gi"}]}}`,
	} {
		if rec := doAutoPatch(h, "localhost:8080", "http://localhost:8080", body); rec.Code != http.StatusBadRequest {
			t.Errorf("PATCH invalid VPA %s = %d %q, want 400", body, rec.Code, rec.Body.String())
		}
	}

	// Accumulating > 32 distinct containerPolicies across sequential PATCH calls is rejected with 400.
	batch1 := make([]string, 0, 32)
	for i := range 32 {
		batch1 = append(batch1, fmt.Sprintf(`{"containerName":"c-%d","maxCPU":"100m","maxMemory":""}`, i))
	}
	orphanCur, _ := dyn.Resource(vpaGVRCheck).Namespace("shop").Get(t.Context(), "orphan", metav1.GetOptions{})
	if r1 := doAutoPatch(h, "localhost:8080", "http://localhost:8080",
		fmt.Sprintf(`{"id":"autoscaling.k8s.io/VerticalPodAutoscaler/shop/orphan","resourceVersion":%q,"vpa":{"updateMode":"Auto","containers":[%s]}}`, orphanCur.GetResourceVersion(), strings.Join(batch1, ","))); r1.Code != http.StatusOK {
		t.Fatalf("PATCH orphan 32 containers = %d %q", r1.Code, r1.Body.String())
	}
	orphan32, _ := dyn.Resource(vpaGVRCheck).Namespace("shop").Get(t.Context(), "orphan", metav1.GetOptions{})
	if r2 := doAutoPatch(h, "localhost:8080", "http://localhost:8080",
		fmt.Sprintf(`{"id":"autoscaling.k8s.io/VerticalPodAutoscaler/shop/orphan","resourceVersion":%q,"vpa":{"updateMode":"Auto","containers":[{"containerName":"c-33","maxCPU":"100m","maxMemory":""}]}}`, orphan32.GetResourceVersion())); r2.Code != http.StatusBadRequest {
		t.Errorf("PATCH orphan 33rd accumulated containerPolicy = %d %q, want 400", r2.Code, r2.Body.String())
	}

	// 5. Missing VPA CRD (NoResourceMatchError / NotFound) maps to 404.
	dyn.PrependReactor("get", "verticalpodautoscalers", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, &meta.NoResourceMatchError{PartialResource: vpaGVR}
	})
	if rec := doAutoGet(h, "localhost:8080", "autoscaling.k8s.io/VerticalPodAutoscaler/shop/bare"); rec.Code != http.StatusNotFound {
		t.Errorf("missing VPA CRD = %d %q, want 404", rec.Code, rec.Body.String())
	}
}

func TestAutoscalerReplayDeltas(t *testing.T) {
	hub := stream.NewHub(stream.HubOptions{})
	_ = hub.Publish(model.Event{
		Type: stream.EventSnapshot,
		Data: model.SnapshotData{
			Entities: []model.Entity{
				{ID: "app/shop/Deployment/web", Group: "apps", Kind: "Deployment", Namespace: "shop", Name: "web", Status: "2/2 ready"},
				{ID: "autoscaling/HorizontalPodAutoscaler/shop/web", Group: "autoscaling", Kind: "HorizontalPodAutoscaler", Namespace: "shop", Name: "web", AppID: "app/shop/Deployment/web", Status: "2 replicas (min 1, max 10)"},
				{ID: "autoscaling.k8s.io/VerticalPodAutoscaler/shop/worker", Group: "autoscaling.k8s.io", Kind: "VerticalPodAutoscaler", Namespace: "shop", Name: "worker", AppID: "app/shop/Deployment/worker", Status: "Auto"},
			},
		},
	})
	h := NewReplayServer(hub, false).Handler()

	// Patch HPA -> Status becomes "2/3–12".
	hpaRec := doAutoPatch(h, "127.0.0.1:18090", "http://127.0.0.1:18090",
		`{"id":"autoscaling/HorizontalPodAutoscaler/shop/web","resourceVersion":"1","hpa":{"minReplicas":3,"maxReplicas":12}}`)
	if hpaRec.Code != http.StatusOK {
		t.Fatalf("replay PATCH HPA = %d %q", hpaRec.Code, hpaRec.Body.String())
	}
	if ent, _ := hub.Entity("autoscaling/HorizontalPodAutoscaler/shop/web"); ent.Status != "2/3–12" {
		t.Errorf("replay HPA entity Status = %q, want %q", ent.Status, "2/3–12")
	}

	// Patch VPA -> Status becomes "Recreate".
	vpaRec := doAutoPatch(h, "127.0.0.1:18090", "http://127.0.0.1:18090",
		`{"id":"autoscaling.k8s.io/VerticalPodAutoscaler/shop/worker","resourceVersion":"1","vpa":{"updateMode":"Recreate","containers":[{"containerName":"worker","maxCPU":"1","maxMemory":"512Mi"}]}}`)
	if vpaRec.Code != http.StatusOK {
		t.Fatalf("replay PATCH VPA = %d %q", vpaRec.Code, vpaRec.Body.String())
	}
	if ent, _ := hub.Entity("autoscaling.k8s.io/VerticalPodAutoscaler/shop/worker"); ent.Status != "Recreate" {
		t.Errorf("replay VPA entity Status = %q, want %q", ent.Status, "Recreate")
	}
}
