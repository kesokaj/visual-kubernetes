package demo

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/streaming/pkg/httpstream"

	"github.com/kesokaj/visual-kubernetes/internal/model"
)

// KubeExec implements PodExecutor against the live model's cached Pods (D9) and an untimed
// client-go rest.Config (D8) with WebSocket-to-SPDY fallback.
type KubeExec struct {
	Model   *model.Model
	Client  kubernetes.Interface
	ExecCfg *rest.Config
	Log     *slog.Logger
}

// ResolvePod checks that ref names a Running, isolated non-protected Pod in Model (refusing
// hostNetwork, hostPID, hostIPC, hostPath, privileged, and elevated-capability Pods) and selects a
// valid application container (defaulting to the first container when container is empty).
func (e *KubeExec) ResolvePod(ref model.Ref, container string) (PodTarget, *StatusErr) {
	if e == nil || e.Model == nil {
		return PodTarget{}, &StatusErr{Code: http.StatusServiceUnavailable, Msg: "live model unavailable"}
	}
	if IsProtectedNamespace(ref.Namespace) {
		return PodTarget{}, &StatusErr{Code: http.StatusForbidden, Msg: "protected namespace is refused in demo mode"}
	}
	raw, ok := e.Model.Object(ref.ID())
	if !ok {
		return PodTarget{}, &StatusErr{Code: http.StatusNotFound, Msg: "pod not found"}
	}
	pod, ok := raw.(*corev1.Pod)
	if !ok || pod == nil {
		return PodTarget{}, &StatusErr{Code: http.StatusNotFound, Msg: "pod not found"}
	}
	if pod.Status.Phase != corev1.PodRunning {
		return PodTarget{}, &StatusErr{Code: http.StatusConflict, Msg: "pod is not running"}
	}
	if reason, ok := checkPodIsolation(&pod.Spec); !ok {
		return PodTarget{}, &StatusErr{Code: http.StatusForbidden, Msg: reason}
	}
	chosen, names, st := selectPodContainer(pod.Spec.Containers, container)
	if st != nil {
		return PodTarget{}, st
	}
	return PodTarget{Namespace: pod.Namespace, Pod: pod.Name, Container: chosen, Containers: names}, nil
}

// checkPodIsolation refuses Pods that share host namespaces, mount hostPath volumes, or run
// privileged or non-baseline-capability containers, since an interactive shell inside them reaches
// node-level networks, processes, IPC, filesystems, or kernel capabilities.
func checkPodIsolation(spec *corev1.PodSpec) (string, bool) {
	switch {
	case spec.HostNetwork:
		return "hostNetwork pods are refused in demo mode", false
	case spec.HostPID:
		return "hostPID pods are refused in demo mode", false
	case spec.HostIPC:
		return "hostIPC pods are refused in demo mode", false
	case hasHostPathVolume(spec.Volumes):
		return "hostPath pods are refused in demo mode", false
	case anySecurityContext(spec, isPrivileged):
		return "privileged pods are refused in demo mode", false
	case anySecurityContext(spec, hasNonBaselineCapability):
		return "elevated-capability pods are refused in demo mode", false
	default:
		return "", true
	}
}

func hasHostPathVolume(vols []corev1.Volume) bool {
	for i := range vols {
		if vols[i].HostPath != nil {
			return true
		}
	}
	return false
}

func anySecurityContext(spec *corev1.PodSpec, match func(*corev1.SecurityContext) bool) bool {
	for i := range spec.Containers {
		if match(spec.Containers[i].SecurityContext) {
			return true
		}
	}
	for i := range spec.InitContainers {
		if match(spec.InitContainers[i].SecurityContext) {
			return true
		}
	}
	for i := range spec.EphemeralContainers {
		if match(spec.EphemeralContainers[i].SecurityContext) {
			return true
		}
	}
	return false
}

func isPrivileged(sc *corev1.SecurityContext) bool {
	return sc != nil && sc.Privileged != nil && *sc.Privileged
}

// isBaselineCapability matches the Kubernetes Pod Security Standards Baseline allowlist for
// securityContext.capabilities.add; capabilities outside it (such as SYS_ADMIN, SYS_MODULE,
// SYS_PTRACE, NET_ADMIN, BPF, DAC_READ_SEARCH, or ALL) break container isolation.
func isBaselineCapability(c corev1.Capability) bool {
	name := strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(string(c))), "CAP_")
	switch name {
	case "AUDIT_WRITE", "CHOWN", "DAC_OVERRIDE", "FOWNER", "FSETID",
		"KILL", "MKNOD", "NET_BIND_SERVICE", "SETFCAP", "SETGID",
		"SETPCAP", "SETUID", "SYS_CHROOT":
		return true
	default:
		return false
	}
}

func hasNonBaselineCapability(sc *corev1.SecurityContext) bool {
	if sc == nil || sc.Capabilities == nil {
		return false
	}
	for _, c := range sc.Capabilities.Add {
		if !isBaselineCapability(c) {
			return true
		}
	}
	return false
}

func selectPodContainer(ctrs []corev1.Container, requested string) (string, []string, *StatusErr) {
	if len(ctrs) == 0 {
		return "", nil, &StatusErr{Code: http.StatusBadRequest, Msg: "pod has no containers"}
	}
	names := make([]string, 0, len(ctrs))
	chosen := ""
	for _, c := range ctrs {
		names = append(names, c.Name)
		if requested == "" && chosen == "" {
			chosen = c.Name
		} else if c.Name == requested {
			chosen = c.Name
		}
	}
	if chosen == "" {
		return "", nil, &StatusErr{Code: http.StatusBadRequest, Msg: "unknown container"}
	}
	return chosen, names, nil
}

// Exec opens a remotecommand PTY stream (WebSocket primary, SPDY fallback) over ExecCfg.
func (e *KubeExec) Exec(ctx context.Context, req ExecRequest) error {
	if e == nil || e.Client == nil || e.ExecCfg == nil {
		return fmt.Errorf("kubernetes exec client unavailable")
	}
	restReq := e.Client.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(req.Target.Pod).
		Namespace(req.Target.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: req.Target.Container,
			Command:   []string{req.Shell},
			Stdin:     true,
			Stdout:    true,
			Stderr:    true,
			TTY:       true,
		}, scheme.ParameterCodec)

	execURL := restReq.URL()
	wsExec, err := remotecommand.NewWebSocketExecutor(e.ExecCfg, "GET", execURL.String())
	if err != nil {
		return fmt.Errorf("building websocket executor: %w", err)
	}
	spdyExec, err := remotecommand.NewSPDYExecutor(e.ExecCfg, "POST", execURL)
	if err != nil {
		return fmt.Errorf("building spdy executor: %w", err)
	}
	exec, err := remotecommand.NewFallbackExecutor(wsExec, spdyExec, func(err error) bool {
		return httpstream.IsUpgradeFailure(err) || httpstream.IsHTTPSProxyError(err)
	})
	if err != nil {
		return fmt.Errorf("building fallback executor: %w", err)
	}
	return exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:             req.Stdin,
		Stdout:            req.Stdout,
		Stderr:            req.Stderr,
		Tty:               true,
		TerminalSizeQueue: req.SizeQueue,
	})
}
