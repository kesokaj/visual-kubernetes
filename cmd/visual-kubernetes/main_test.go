package main

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/client-go/rest"

	"github.com/kesokaj/visual-kubernetes/internal/probe"
)

func TestRunUsage(t *testing.T) {
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	for _, args := range [][]string{{"serve"}, {"--bogus"}, {"probe", "--bogus"}, {"probe", "extra"}} {
		var stderr bytes.Buffer
		if code := run(t.Context(), args, io.Discard, &stderr, log); code != 2 {
			t.Errorf("run(%q) = %d, want 2", args, code)
		}
	}
}

func TestRestConfigMissingKubeconfigFails(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	_, _, err := restConfig(filepath.Join(t.TempDir(), "absent"))
	if err == nil || !strings.Contains(err.Error(), "absent") {
		t.Fatalf("restConfig(absent) err = %v, want a not-found error naming the path", err)
	}
}

// TestKubeletClientNeverBuiltFromKubeconfig: operator credentials from a kubeconfig must never be
// presented to a kubelet, even when the config is otherwise a valid one for kubelet.New.
func TestKubeletClientNeverBuiltFromKubeconfig(t *testing.T) {
	cfg := &rest.Config{BearerToken: "operator-admin-token", TLSClientConfig: rest.TLSClientConfig{CAData: []byte("ca")}}
	kc, err := kubeletClient(cfg, false)
	if kc != nil || !errors.Is(err, probe.ErrLocalMode) {
		t.Fatalf("kubeletClient(local) = %v, %v; want nil, ErrLocalMode", kc, err)
	}
}
