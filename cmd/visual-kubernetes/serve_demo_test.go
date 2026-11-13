package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	dynfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/kesokaj/visual-kubernetes/internal/demo"
	"github.com/kesokaj/visual-kubernetes/internal/model"
	"github.com/kesokaj/visual-kubernetes/internal/stream"
)

func TestServeDemoDisabledRejectsAllDemoRoutes(t *testing.T) {
	a, err := startReplayOpts(t.Context(), "../../test/fixtures/streams/deploy-scale.ndjson", true, false, discard())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/demo/services/type?id=core/Service/default/web", ""},
		{http.MethodPatch, "/api/demo/services/type", `{"id":"core/Service/default/web","resourceVersion":"1","type":"LoadBalancer"}`},
		{http.MethodGet, "/api/demo/workloads/scale?id=apps/Deployment/default/web", ""},
		{http.MethodPatch, "/api/demo/workloads/scale", `{"id":"apps/Deployment/default/web","resourceVersion":"1","replicas":3}`},
		{http.MethodGet, "/api/demo/autoscalers?id=autoscaling/HorizontalPodAutoscaler/default/web", ""},
		{http.MethodPatch, "/api/demo/autoscalers", `{"id":"autoscaling/HorizontalPodAutoscaler/default/web","resourceVersion":"1","hpa":{"minReplicas":1,"maxReplicas":5}}`},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Host = "127.0.0.1:8080"
		if tc.method == http.MethodPatch {
			req.Header.Set("Origin", "http://127.0.0.1:8080")
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		a.handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "demo mode is off") {
			t.Errorf("%s %s with --demo=false = %d %q, want 403 demo mode is off", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
}

func TestServeDemoLiveWiringWiresServiceScaleAndAutoscalerStores(t *testing.T) {
	two := int32(2)
	one := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "web", ResourceVersion: "11"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &two,
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "web", Image: "nginx"}}},
			},
		},
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "web", ResourceVersion: "22"},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: "10.96.1.10",
			Selector:  map[string]string{"app": "web"},
			Ports: []corev1.ServicePort{{
				Name: "http", Port: 80, TargetPort: intstr.FromInt32(8080), Protocol: corev1.ProtocolTCP,
			}},
		},
	}
	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "web-hpa", ResourceVersion: "33"},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{APIVersion: "apps/v1", Kind: "Deployment", Name: "web"},
			MinReplicas:    &one,
			MaxReplicas:    8,
		},
	}
	vpa := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "autoscaling.k8s.io/v1",
		"kind":       "VerticalPodAutoscaler",
		"metadata":   map[string]any{"namespace": "shop", "name": "web-vpa", "resourceVersion": "44"},
		"spec": map[string]any{
			"targetRef": map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "web"},
		},
	}}

	m := model.New()
	m.Apply(
		model.Change{Obj: dep.DeepCopy()},
		model.Change{Obj: svc.DeepCopy()},
		model.Change{Obj: hpa.DeepCopy()},
		model.Change{Obj: vpa.DeepCopy()},
	)

	scheme := runtime.NewScheme()
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		{Group: "autoscaling.k8s.io", Version: "v1", Resource: "verticalpodautoscalers"}: "VerticalPodAutoscalerList",
	}, vpa)
	cl := clients{
		inCluster: true,
		cs:        fake.NewClientset(dep, svc, hpa),
		dyn:       dyn,
	}
	hub := stream.NewHub(stream.HubOptions{})
	_ = hub.Publish(m.SnapshotEvent(map[string]model.Capability{demo.SourceName: demo.Capability(true)}))
	srv := newLiveServer(hub, func() bool { return true }, m, cl, true, discard())
	srv.Demo = newLiveDemoHandler(m, cl, false, discard())
	h := srv.Handler()

	// 1. ServiceStore wired: GET /api/demo/services/type -> 200.
	svcReq := httptest.NewRequest(http.MethodGet, "/api/demo/services/type?id=core/Service/shop/web", nil)
	svcReq.Host = "127.0.0.1:8080"
	svcRec := httptest.NewRecorder()
	h.ServeHTTP(svcRec, svcReq)
	if svcRec.Code != http.StatusOK {
		t.Fatalf("live GET /api/demo/services/type = %d %q, want 200", svcRec.Code, svcRec.Body.String())
	}

	// 2. KubeWorkloads.Model wired: GET /api/demo/workloads/scale populates managedByHPA="web-hpa".
	scaleReq := httptest.NewRequest(http.MethodGet, "/api/demo/workloads/scale?id=apps/Deployment/shop/web", nil)
	scaleReq.Host = "127.0.0.1:8080"
	scaleRec := httptest.NewRecorder()
	h.ServeHTTP(scaleRec, scaleReq)
	if scaleRec.Code != http.StatusOK {
		t.Fatalf("live GET /api/demo/workloads/scale = %d %q, want 200", scaleRec.Code, scaleRec.Body.String())
	}
	var scaleResp demo.WorkloadScaleResponse
	_ = json.Unmarshal(scaleRec.Body.Bytes(), &scaleResp)
	if scaleResp.ManagedByHPA != "web-hpa" || scaleResp.Replicas != 2 {
		t.Fatalf("live scale response = %+v, want replicas=2 managedByHPA=web-hpa", scaleResp)
	}

	// 3. KubeAutoscalers (Client + Dyn + Model) wired: both HPA and VPA return 200 (with VPA container seeded from Model).
	for _, id := range []string{
		"autoscaling/HorizontalPodAutoscaler/shop/web-hpa",
		"autoscaling.k8s.io/VerticalPodAutoscaler/shop/web-vpa",
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/demo/autoscalers?id="+id, nil)
		req.Host = "127.0.0.1:8080"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("live GET /api/demo/autoscalers?id=%s = %d %q, want 200", id, rec.Code, rec.Body.String())
		}
	}
}
