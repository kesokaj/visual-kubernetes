package demo

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
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

func seedContainerSpec() corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "app",
					EnvFrom: []corev1.EnvFromSource{
						{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "base-cm"}}},
					},
					Env: []corev1.EnvVar{
						{Name: "LOG_LEVEL", Value: "info"},
						{Name: "EMPTY_PLAIN", Value: ""},
						{
							Name: "API_SECRET",
							ValueFrom: &corev1.EnvVarSource{
								SecretKeyRef: &corev1.SecretKeySelector{
									LocalObjectReference: corev1.LocalObjectReference{Name: "app-secrets"},
									Key:                  "api-key",
								},
							},
						},
						{
							Name: "CONFIG_MODE",
							ValueFrom: &corev1.EnvVarSource{
								ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
									LocalObjectReference: corev1.LocalObjectReference{Name: "app-cfg"},
									Key:                  "mode",
								},
							},
						},
						{
							Name: "POD_IP",
							ValueFrom: &corev1.EnvVarSource{
								FieldRef: &corev1.ObjectFieldSelector{FieldPath: "status.podIP"},
							},
						},
						{
							Name: "MEM_LIMIT",
							ValueFrom: &corev1.EnvVarSource{
								ResourceFieldRef: &corev1.ResourceFieldSelector{Resource: "limits.memory"},
							},
						},
					},
				},
				{
					Name: "sidecar",
					Env:  []corev1.EnvVar{{Name: "PROXY_PORT", Value: "15001"}},
				},
			},
		},
	}
}

func newFakeWorkloadServer(t *testing.T, objs ...runtime.Object) (*fake.Clientset, http.Handler) {
	t.Helper()
	cs := fake.NewClientset(objs...)
	srv := NewServer(Options{
		Catalog:   &ModelCatalog{Model: model.New()},
		Workloads: &KubeWorkloads{Client: cs},
	})
	return cs, srv.Handler()
}

func doEnvGet(h http.Handler, host, id string) *httptest.ResponseRecorder {
	u := "/api/demo/workloads/env?id=" + id
	req := httptest.NewRequest(http.MethodGet, u, nil)
	req.Host = host
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func doEnvPatch(h http.Handler, host, origin, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPatch, "/api/demo/workloads/env", strings.NewReader(body))
	req.Host = host
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestWorkloadEnvGetAndPatchAllKinds(t *testing.T) {
	meta := metav1.ObjectMeta{Namespace: "shop", Name: "svc", ResourceVersion: "1042"}
	dep := &appsv1.Deployment{ObjectMeta: meta, Spec: appsv1.DeploymentSpec{Template: seedContainerSpec()}}
	sts := &appsv1.StatefulSet{ObjectMeta: meta, Spec: appsv1.StatefulSetSpec{Template: seedContainerSpec()}}
	ds := &appsv1.DaemonSet{ObjectMeta: meta, Spec: appsv1.DaemonSetSpec{Template: seedContainerSpec()}}

	cs, h := newFakeWorkloadServer(t, dep, sts, ds)

	for _, id := range []string{
		"apps/Deployment/shop/svc",
		"apps/StatefulSet/shop/svc",
		"apps/DaemonSet/shop/svc",
	} {
		t.Run(id, func(t *testing.T) {
			getRec := doEnvGet(h, "localhost:8080", id)
			if getRec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d %q, want 200", id, getRec.Code, getRec.Body.String())
			}
			var got WorkloadEnvResponse
			if err := json.Unmarshal(getRec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode GET %s: %v", id, err)
			}
			if got.ID != id || got.ResourceVersion != "1042" || len(got.Containers) != 2 {
				t.Fatalf("GET payload = %+v, want id=%s rv=1042 2 containers", got, id)
			}
			appEnv := got.Containers[0].Env
			if len(appEnv) != 6 {
				t.Fatalf("app env len = %d, want 6: %+v", len(appEnv), appEnv)
			}
			if appEnv[0].Value == nil || *appEnv[0].Value != "info" || appEnv[0].ReadOnlySource != "" {
				t.Errorf("LOG_LEVEL = %+v, want plain value 'info'", appEnv[0])
			}
			if appEnv[1].Value == nil || *appEnv[1].Value != "" || !bytes.Contains(getRec.Body.Bytes(), []byte(`"name":"EMPTY_PLAIN","value":""`)) {
				t.Errorf("EMPTY_PLAIN = %+v, want explicit empty string value in JSON", appEnv[1])
			}
			wantSources := []string{
				"secret:app-secrets/api-key",
				"configMap:app-cfg/mode",
				"fieldRef:status.podIP",
				"resourceFieldRef:limits.memory",
			}
			for i, ws := range wantSources {
				if appEnv[i+2].Value != nil || appEnv[i+2].ReadOnlySource != ws {
					t.Errorf("env[%d] = %+v, want readOnlySource %q and nil Value", i+2, appEnv[i+2], ws)
				}
			}

			patchBody := fmt.Sprintf(`{"id":%q,"resourceVersion":"1042","container":"app","env":[{"name":"LOG_LEVEL","value":"debug"},{"name":"NEW_FLAG","value":"true"}]}`, id)
			patchRec := doEnvPatch(h, "localhost:8080", "http://localhost:8080", patchBody)
			if patchRec.Code != http.StatusOK {
				t.Fatalf("PATCH %s = %d %q, want 200", id, patchRec.Code, patchRec.Body.String())
			}
			var patched WorkloadEnvResponse
			if err := json.Unmarshal(patchRec.Body.Bytes(), &patched); err != nil {
				t.Fatalf("decode PATCH %s: %v", id, err)
			}
			gotNames := make([]string, 0, len(patched.Containers[0].Env))
			for _, ev := range patched.Containers[0].Env {
				gotNames = append(gotNames, ev.Name)
			}
			// LOG_LEVEL stays first, EMPTY_PLAIN removed, all 4 valueFrom entries preserved in order, NEW_FLAG appended.
			wantOrder := "LOG_LEVEL,API_SECRET,CONFIG_MODE,POD_IP,MEM_LIMIT,NEW_FLAG"
			if strings.Join(gotNames, ",") != wantOrder {
				t.Errorf("patched env order = %q, want %q", strings.Join(gotNames, ","), wantOrder)
			}
			if patched.Containers[1].Env[0].Value == nil || *patched.Containers[1].Env[0].Value != "15001" {
				t.Errorf("sidecar container changed = %+v", patched.Containers[1])
			}
		})
	}

	// Confirm FieldManager == "visual-kubernetes-demo", zero Secret reads (R2), and EnvFrom preserved.
	var updateCount int
	for _, act := range cs.Actions() {
		if act.GetResource().Resource == "secrets" {
			t.Errorf("unexpected Secret action: %+v", act)
		}
		if u, ok := act.(k8stesting.UpdateActionImpl); ok {
			updateCount++
			if u.GetUpdateOptions().FieldManager != FieldManagerName {
				t.Errorf("FieldManager = %q, want %q", u.GetUpdateOptions().FieldManager, FieldManagerName)
			}
		}
	}
	if updateCount != 3 {
		t.Errorf("update actions = %d, want 3", updateCount)
	}
	storedDep, err := cs.AppsV1().Deployments("shop").Get(t.Context(), "svc", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(storedDep.Spec.Template.Spec.Containers[0].EnvFrom) != 1 {
		t.Errorf("EnvFrom lost on Deployment update: %+v", storedDep.Spec.Template.Spec.Containers[0].EnvFrom)
	}
}

func TestWorkloadEnvZeroInitialEnvAddAndClear(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "bare", ResourceVersion: "7"},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "web"}}},
			},
		},
	}
	_, h := newFakeWorkloadServer(t, dep)

	rec := doEnvPatch(h, "localhost:8080", "http://localhost:8080",
		`{"id":"apps/Deployment/shop/bare","resourceVersion":"7","container":"web","env":[{"name":"PORT","value":"8080"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("add to empty env = %d %q, want 200", rec.Code, rec.Body.String())
	}

	clearRec := doEnvPatch(h, "localhost:8080", "http://localhost:8080",
		`{"id":"apps/Deployment/shop/bare","resourceVersion":"7","container":"web","env":[]}`)
	if clearRec.Code != http.StatusOK {
		t.Fatalf("clear plain env = %d %q, want 200", clearRec.Code, clearRec.Body.String())
	}
	var got WorkloadEnvResponse
	_ = json.Unmarshal(clearRec.Body.Bytes(), &got)
	if len(got.Containers[0].Env) != 0 {
		t.Errorf("cleared env = %+v, want empty", got.Containers[0].Env)
	}
}

func TestWorkloadEnvValidationAndReadOnlyProtection(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "web", ResourceVersion: "10"},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: "web",
							Env: []corev1.EnvVar{
								{
									Name: "SECRET_KEY",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{Name: "s"},
											Key:                  "k",
										},
									},
								},
								{
									Name:      "FUTURE_SOURCE",
									ValueFrom: &corev1.EnvVarSource{},
								},
							},
						},
					},
				},
			},
		},
	}
	cs, h := newFakeWorkloadServer(t, dep)

	tooMany := make([]string, 129)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf(`{"name":"VAR_%d","value":"1"}`, i)
	}
	longName := strings.Repeat("A", 254)

	cases := []struct {
		name string
		body string
	}{
		{"touch secretKeyRef", `{"id":"apps/Deployment/shop/web","resourceVersion":"10","container":"web","env":[{"name":"SECRET_KEY","value":"override"}]}`},
		{"touch unknown non-nil valueFrom", `{"id":"apps/Deployment/shop/web","resourceVersion":"10","container":"web","env":[{"name":"FUTURE_SOURCE","value":"override"}]}`},
		{"empty var name", `{"id":"apps/Deployment/shop/web","resourceVersion":"10","container":"web","env":[{"name":"","value":"1"}]}`},
		{"digit prefix", `{"id":"apps/Deployment/shop/web","resourceVersion":"10","container":"web","env":[{"name":"1BAD","value":"1"}]}`},
		{"hyphen in name", `{"id":"apps/Deployment/shop/web","resourceVersion":"10","container":"web","env":[{"name":"BAD-NAME","value":"1"}]}`},
		{"254 char name", fmt.Sprintf(`{"id":"apps/Deployment/shop/web","resourceVersion":"10","container":"web","env":[{"name":%q,"value":"1"}]}`, longName)},
		{"duplicate var", `{"id":"apps/Deployment/shop/web","resourceVersion":"10","container":"web","env":[{"name":"A","value":"1"},{"name":"A","value":"2"}]}`},
		{"over 128 vars", fmt.Sprintf(`{"id":"apps/Deployment/shop/web","resourceVersion":"10","container":"web","env":[%s]}`, strings.Join(tooMany, ","))},
		{"missing value field", `{"id":"apps/Deployment/shop/web","resourceVersion":"10","container":"web","env":[{"name":"A"}]}`},
		{"missing env array", `{"id":"apps/Deployment/shop/web","resourceVersion":"10","container":"web"}`},
		{"missing resourceVersion", `{"id":"apps/Deployment/shop/web","container":"web","env":[]}`},
		{"unknown container", `{"id":"apps/Deployment/shop/web","resourceVersion":"10","container":"missing","env":[]}`},
		{"app bubble id", `{"id":"app/shop/Deployment/web","resourceVersion":"10","container":"web","env":[]}`},
		{"non-workload kind", `{"id":"apps/ReplicaSet/shop/web-1","resourceVersion":"10","container":"web","env":[]}`},
		{"unknown json field", `{"id":"apps/Deployment/shop/web","resourceVersion":"10","container":"web","env":[],"extra":true}`},
		{"trailing json", `{"id":"apps/Deployment/shop/web","resourceVersion":"10","container":"web","env":[]}{}`},
		{"oversized body", fmt.Sprintf(`{"id":"apps/Deployment/shop/web","resourceVersion":"10","container":"web","env":[{"name":"BIG","value":%q}]}`, strings.Repeat("x", 70*1024))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doEnvPatch(h, "localhost:8080", "http://localhost:8080", tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s: status = %d %q, want 400", tc.name, rec.Code, rec.Body.String())
			}
		})
	}

	for _, act := range cs.Actions() {
		if act.GetVerb() == "update" || act.GetVerb() == "patch" {
			t.Errorf("rejected validation mutated cluster: %+v", act)
		}
	}
}

func TestWorkloadEnvConflict409(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "web", ResourceVersion: "42"},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "web", Env: []corev1.EnvVar{{Name: "A", Value: "1"}}}}},
			},
		},
	}
	cs, h := newFakeWorkloadServer(t, dep)

	// 1. Stale resourceVersion rejected before Update.
	stale := doEnvPatch(h, "localhost:8080", "http://localhost:8080",
		`{"id":"apps/Deployment/shop/web","resourceVersion":"41","container":"web","env":[{"name":"A","value":"2"}]}`)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale rv status = %d %q, want 409", stale.Code, stale.Body.String())
	}
	stored, _ := cs.AppsV1().Deployments("shop").Get(t.Context(), "web", metav1.GetOptions{})
	if stored.Spec.Template.Spec.Containers[0].Env[0].Value != "1" {
		t.Fatalf("stale PATCH mutated Deployment: %+v", stored.Spec.Template.Spec.Containers[0].Env)
	}

	// 2. Concurrent API-server conflict during Update also maps to 409.
	cs.PrependReactor("update", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewConflict(schema.GroupResource{Group: "apps", Resource: "deployments"}, "web", errors.New("concurrent write"))
	})
	raceRec := doEnvPatch(h, "localhost:8080", "http://localhost:8080",
		`{"id":"apps/Deployment/shop/web","resourceVersion":"42","container":"web","env":[{"name":"A","value":"2"}]}`)
	if raceRec.Code != http.StatusConflict {
		t.Fatalf("API conflict status = %d %q, want 409", raceRec.Code, raceRec.Body.String())
	}
}

func TestWorkloadEnvSecurityGuards(t *testing.T) {
	sysDep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system", Name: "coredns", ResourceVersion: "1"},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "coredns"}}}},
		},
	}
	cs, h := newFakeWorkloadServer(t, sysDep)

	for _, ns := range []string{"kube-system", "kube-public", "kube-node-lease", "visual-kubernetes", "gke-managed-dpv2", "gmp-system", "_"} {
		id := "apps/Deployment/" + ns + "/coredns"
		if rec := doEnvGet(h, "localhost:8080", id); rec.Code != http.StatusForbidden {
			t.Errorf("GET protected %s = %d, want 403", id, rec.Code)
		}
		body := fmt.Sprintf(`{"id":%q,"resourceVersion":"1","container":"coredns","env":[]}`, id)
		if rec := doEnvPatch(h, "localhost:8080", "http://localhost:8080", body); rec.Code != http.StatusForbidden {
			t.Errorf("PATCH protected %s = %d, want 403", id, rec.Code)
		}
	}
	if len(cs.Actions()) != 0 {
		t.Errorf("protected namespace reached Kubernetes client: %+v", cs.Actions())
	}

	// SameOrigin required on PATCH.
	if rec := doEnvPatch(h, "localhost:8080", "", `{"id":"apps/Deployment/shop/web","resourceVersion":"1","container":"web","env":[]}`); rec.Code != http.StatusForbidden {
		t.Errorf("missing Origin PATCH = %d, want 403", rec.Code)
	}
	if rec := doEnvPatch(h, "localhost:8080", "https://evil.example", `{"id":"apps/Deployment/shop/web","resourceVersion":"1","container":"web","env":[]}`); rec.Code != http.StatusForbidden {
		t.Errorf("cross-origin PATCH = %d, want 403", rec.Code)
	}

	// Non-loopback Host refused without IAP.
	if rec := doEnvGet(h, "demo.corp.internal:8080", "apps/Deployment/shop/web"); rec.Code != http.StatusForbidden {
		t.Errorf("non-loopback GET = %d, want 403", rec.Code)
	}

	// Missing workload -> 404.
	if rec := doEnvGet(h, "localhost:8080", "apps/Deployment/shop/missing"); rec.Code != http.StatusNotFound {
		t.Errorf("missing workload GET = %d, want 404", rec.Code)
	}
}

func TestWorkloadEnvReplayStore(t *testing.T) {
	hub := stream.NewHub(stream.HubOptions{})
	_ = hub.Publish(model.Event{
		Type: "snapshot",
		Data: model.SnapshotData{
			Entities: []model.Entity{
				{ID: "app/shop/Deployment/web", Kind: "Deployment", Group: "apps", Name: "web", Namespace: "shop", OwnerID: "apps/Deployment/shop/web"},
				{ID: "app/kube-system/Deployment/coredns", Kind: "Deployment", Group: "apps", Name: "coredns", Namespace: "kube-system", OwnerID: "apps/Deployment/kube-system/coredns"},
			},
		},
	})
	h := NewReplayServer(hub, false).Handler()

	getRec := doEnvGet(h, "127.0.0.1:18090", "apps/Deployment/shop/web")
	if getRec.Code != http.StatusOK {
		t.Fatalf("replay GET = %d %q, want 200", getRec.Code, getRec.Body.String())
	}
	var initEnv WorkloadEnvResponse
	if err := json.Unmarshal(getRec.Body.Bytes(), &initEnv); err != nil {
		t.Fatal(err)
	}
	if initEnv.ResourceVersion != "1" || len(initEnv.Containers) != 1 || len(initEnv.Containers[0].Env) < 2 {
		t.Fatalf("unexpected initial replay env = %+v", initEnv)
	}

	// Touching the seeded Secret-backed variable fails with 400.
	badSecret := doEnvPatch(h, "127.0.0.1:18090", "http://127.0.0.1:18090",
		`{"id":"apps/Deployment/shop/web","resourceVersion":"1","container":"web","env":[{"name":"API_SECRET","value":"hacked"}]}`)
	if badSecret.Code != http.StatusBadRequest {
		t.Errorf("replay overwrite secret var = %d, want 400", badSecret.Code)
	}

	// Valid PATCH updates plain var, adds CACHE_TTL, preserves API_SECRET, bumps resourceVersion to "2".
	okPatch := doEnvPatch(h, "127.0.0.1:18090", "http://127.0.0.1:18090",
		`{"id":"apps/Deployment/shop/web","resourceVersion":"1","container":"web","env":[{"name":"LOG_LEVEL","value":"debug"},{"name":"CACHE_TTL","value":"60"}]}`)
	if okPatch.Code != http.StatusOK {
		t.Fatalf("replay PATCH = %d %q, want 200", okPatch.Code, okPatch.Body.String())
	}
	var updated WorkloadEnvResponse
	_ = json.Unmarshal(okPatch.Body.Bytes(), &updated)
	if updated.ResourceVersion != "2" {
		t.Errorf("updated rv = %q, want 2", updated.ResourceVersion)
	}

	// Stale resourceVersion "1" now fails with 409.
	conflictPatch := doEnvPatch(h, "127.0.0.1:18090", "http://127.0.0.1:18090",
		`{"id":"apps/Deployment/shop/web","resourceVersion":"1","container":"web","env":[{"name":"LOG_LEVEL","value":"warn"}]}`)
	if conflictPatch.Code != http.StatusConflict {
		t.Errorf("replay stale PATCH = %d, want 409", conflictPatch.Code)
	}

	// Protected namespace and missing workload.
	if rec := doEnvGet(h, "127.0.0.1:18090", "apps/Deployment/kube-system/coredns"); rec.Code != http.StatusForbidden {
		t.Errorf("replay protected GET = %d, want 403", rec.Code)
	}
	if rec := doEnvGet(h, "127.0.0.1:18090", "apps/Deployment/shop/missing"); rec.Code != http.StatusNotFound {
		t.Errorf("replay missing GET = %d, want 404", rec.Code)
	}
}
