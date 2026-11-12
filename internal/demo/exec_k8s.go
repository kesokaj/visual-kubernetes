package demo

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

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

// ResolvePod checks that ref names a Running non-protected Pod off the host network in Model and
// selects a valid container (defaulting to the first container when container is empty).
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
	// A hostNetwork Pod shares the node's network namespace, so a shell in it reaches node-local
	// services; the demo proxy refuses these Pods for the same reason (endpoints.go).
	if pod.Spec.HostNetwork {
		return PodTarget{}, &StatusErr{Code: http.StatusForbidden, Msg: "hostNetwork pods are refused in demo mode"}
	}
	if len(pod.Spec.Containers) == 0 {
		return PodTarget{}, &StatusErr{Code: http.StatusBadRequest, Msg: "pod has no containers"}
	}
	names := make([]string, 0, len(pod.Spec.Containers))
	chosen := ""
	for _, c := range pod.Spec.Containers {
		names = append(names, c.Name)
		if container == "" && chosen == "" {
			chosen = c.Name
		} else if c.Name == container {
			chosen = c.Name
		}
	}
	if chosen == "" {
		return PodTarget{}, &StatusErr{Code: http.StatusBadRequest, Msg: "unknown container"}
	}
	return PodTarget{Namespace: pod.Namespace, Pod: pod.Name, Container: chosen, Containers: names}, nil
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
