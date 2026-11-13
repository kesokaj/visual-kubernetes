package demo

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/websocket"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

func unstructuredRoundTripPod(t testing.TB, pod *corev1.Pod) *corev1.Pod {
	t.Helper()
	raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(pod)
	if err != nil {
		t.Fatalf("ToUnstructured(%s): %v", pod.Name, err)
	}
	out := new(corev1.Pod)
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(raw, out); err != nil {
		t.Fatalf("FromUnstructured(%s): %v", pod.Name, err)
	}
	return out
}

func runningIsolationPod(name string, spec corev1.PodSpec) *corev1.Pod {
	return &corev1.Pod{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: name, UID: types.UID("uid-" + name)},
		Spec:       spec,
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func TestPodExecHostAndPrivilegedIsolation(t *testing.T) {
	m := model.New()
	pods := []*corev1.Pod{
		runningIsolationPod("hostpid-0", corev1.PodSpec{
			HostPID:    true,
			Containers: []corev1.Container{{Name: "app"}},
		}),
		runningIsolationPod("hostipc-0", corev1.PodSpec{
			HostIPC:    true,
			Containers: []corev1.Container{{Name: "app"}},
		}),
		runningIsolationPod("priv-main-0", corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:            "app",
				SecurityContext: &corev1.SecurityContext{Privileged: new(true)},
			}},
		}),
		runningIsolationPod("priv-sidecar-0", corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "web", SecurityContext: &corev1.SecurityContext{Privileged: new(false)}},
				{Name: "debug-sidecar", SecurityContext: &corev1.SecurityContext{Privileged: new(true)}},
			},
		}),
		runningIsolationPod("priv-init-0", corev1.PodSpec{
			InitContainers: []corev1.Container{{
				Name:            "init-sysctl",
				SecurityContext: &corev1.SecurityContext{Privileged: new(true)},
			}},
			Containers: []corev1.Container{{Name: "web"}},
		}),
		runningIsolationPod("priv-ephemeral-0", corev1.PodSpec{
			Containers: []corev1.Container{{Name: "web"}},
			EphemeralContainers: []corev1.EphemeralContainer{{
				EphemeralContainerCommon: corev1.EphemeralContainerCommon{
					Name:            "debugger",
					SecurityContext: &corev1.SecurityContext{Privileged: new(true)},
				},
			}},
		}),
		runningIsolationPod("hostpath-0", corev1.PodSpec{
			Volumes: []corev1.Volume{{
				Name:         "node-root",
				VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/var/run"}},
			}},
			Containers: []corev1.Container{{Name: "web"}},
		}),
		runningIsolationPod("cap-sys-admin-0", corev1.PodSpec{
			Containers: []corev1.Container{{
				Name: "web",
				SecurityContext: &corev1.SecurityContext{
					Capabilities: &corev1.Capabilities{Add: []corev1.Capability{"CAP_SYS_ADMIN"}},
				},
			}},
		}),
		runningIsolationPod("cap-init-module-0", corev1.PodSpec{
			InitContainers: []corev1.Container{{
				Name: "init-kmod",
				SecurityContext: &corev1.SecurityContext{
					Capabilities: &corev1.Capabilities{Add: []corev1.Capability{"SYS_MODULE"}},
				},
			}},
			Containers: []corev1.Container{{Name: "web"}},
		}),
		runningIsolationPod("cap-ephemeral-all-0", corev1.PodSpec{
			Containers: []corev1.Container{{Name: "web"}},
			EphemeralContainers: []corev1.EphemeralContainer{{
				EphemeralContainerCommon: corev1.EphemeralContainerCommon{
					Name: "debugger",
					SecurityContext: &corev1.SecurityContext{
						Capabilities: &corev1.Capabilities{Add: []corev1.Capability{"ALL"}},
					},
				},
			}},
		}),
		runningIsolationPod("explicit-unpriv-0", corev1.PodSpec{
			Volumes: []corev1.Volume{{
				Name:         "scratch",
				VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
			}},
			InitContainers: []corev1.Container{{
				Name:            "init",
				SecurityContext: &corev1.SecurityContext{Privileged: new(false)},
			}},
			Containers: []corev1.Container{{
				Name: "web",
				SecurityContext: &corev1.SecurityContext{
					Privileged: new(false),
					Capabilities: &corev1.Capabilities{
						Drop: []corev1.Capability{"ALL"},
						Add:  []corev1.Capability{"NET_BIND_SERVICE", "CAP_CHOWN"},
					},
				},
			}},
			EphemeralContainers: []corev1.EphemeralContainer{{
				EphemeralContainerCommon: corev1.EphemeralContainerCommon{
					Name:            "diag",
					SecurityContext: &corev1.SecurityContext{Privileged: new(false)},
				},
			}},
		}),
	}
	for _, p := range pods {
		m.Apply(model.Change{Obj: unstructuredRoundTripPod(t, p)})
	}

	srv := NewServer(Options{Exec: &fakePodExec{model: m}})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	refused := []struct {
		name     string
		query    string
		wantText string
	}{
		{name: "hostPID pod", query: "id=core/Pod/shop/hostpid-0", wantText: "hostPID"},
		{name: "hostIPC pod", query: "id=core/Pod/shop/hostipc-0", wantText: "hostIPC"},
		{name: "privileged target container", query: "id=core/Pod/shop/priv-main-0", wantText: "privileged"},
		{name: "privileged sibling container when targeting unprivileged container", query: "id=core/Pod/shop/priv-sidecar-0&container=web", wantText: "privileged"},
		{name: "privileged initContainer", query: "id=core/Pod/shop/priv-init-0", wantText: "privileged"},
		{name: "privileged ephemeralContainer", query: "id=core/Pod/shop/priv-ephemeral-0", wantText: "privileged"},
		{name: "hostPath volume pod", query: "id=core/Pod/shop/hostpath-0", wantText: "hostPath"},
		{name: "CAP_SYS_ADMIN container", query: "id=core/Pod/shop/cap-sys-admin-0", wantText: "capability"},
		{name: "SYS_MODULE initContainer", query: "id=core/Pod/shop/cap-init-module-0", wantText: "capability"},
		{name: "ALL capability ephemeralContainer", query: "id=core/Pod/shop/cap-ephemeral-all-0", wantText: "capability"},
	}

	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+"/api/demo/exec?"+tc.query, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Origin", ts.URL)
			resp, err := ts.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("status = %d (%q), want %d", resp.StatusCode, string(body), http.StatusForbidden)
			}
			if !strings.Contains(string(body), tc.wantText) {
				t.Errorf("body = %q, want substring %q", string(body), tc.wantText)
			}
		})
	}

	t.Run("explicit privileged=false remains allowed", func(t *testing.T) {
		ws := dialTestExecWS(t, ts.URL, "id=core/Pod/shop/explicit-unpriv-0", ts.URL)
		var ready execServerFrame
		if err := websocket.JSON.Receive(ws, &ready); err != nil {
			t.Fatalf("receive ready: %v", err)
		}
		if ready.Type != "ready" || ready.Container != "web" {
			t.Errorf("ready frame = %+v, want ready on container web", ready)
		}
		_ = websocket.JSON.Send(ws, execClientFrame{Type: "stdin", Data: "exit\n"})
	})
}
