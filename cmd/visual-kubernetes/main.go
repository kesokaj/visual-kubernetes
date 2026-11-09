// Command visual-kubernetes is the read-only cluster visualizer. Phase 0 ships only the
// `probe` subcommand; the UI server lands in Phase 2 (PLAN-2026-11-09-1906).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/kesokaj/visual-kubernetes/internal/kubelet"
	"github.com/kesokaj/visual-kubernetes/internal/probe"
)

const usage = `usage: visual-kubernetes probe [--kubeconfig PATH]

probe  print one line per data source: <source> ok|missing|denied|unreachable|error <detail>
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr, log))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, log *slog.Logger) int {
	if len(args) == 0 || args[0] != "probe" {
		_, _ = fmt.Fprint(stderr, usage) // usage text; nothing to do if stderr fails
		return 2
	}
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	fs.SetOutput(stderr)
	kubeconfig := fs.String("kubeconfig", "", "path to a kubeconfig; default: in-cluster when KUBERNETES_SERVICE_HOST is set, else the standard loading rules")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if err := runProbe(ctx, *kubeconfig, stdout, log); err != nil {
		log.Error("probe failed", "error", err.Error())
		return 1
	}
	return 0
}

// runProbe exits 0 whatever the sources report (the states are the output); it fails only when
// no API client can be built at all.
func runProbe(ctx context.Context, kubeconfig string, stdout io.Writer, log *slog.Logger) error {
	cfg, inCluster, err := restConfig(kubeconfig)
	if err != nil {
		return fmt.Errorf("loading cluster config: %w", err)
	}
	cfg.Timeout = 15 * time.Second
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("building client: %w", err)
	}
	deps := probe.Deps{
		Client: cs,
		Raw:    probe.RESTRaw{Client: cs.Discovery().RESTClient()},
		Dial:   (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
	}
	deps.Kubelet, deps.KubeletErr = kubeletClient(cfg, inCluster)
	log.Info("probing", "host", cfg.Host)
	return probe.Write(stdout, probe.Run(ctx, deps))
}

// kubeletClient builds the kubelet client only from in-cluster config, whose credentials are the
// app's own read-only ServiceAccount. A kubeconfig carries the operator's credentials, which must
// never be presented to a node: a compromised node holds a valid kubelet cert and could replay them.
func kubeletClient(cfg *rest.Config, inCluster bool) (probe.SummaryFetcher, error) {
	if !inCluster {
		return nil, probe.ErrLocalMode
	}
	kc, err := kubelet.New(cfg, 0, 10*time.Second)
	if err != nil {
		return nil, err
	}
	return kc, nil
}

// restConfig follows the plan's CLI rule: an explicit kubeconfig wins; otherwise in-cluster when
// KUBERNETES_SERVICE_HOST is set; otherwise the standard loading rules (KUBECONFIG, ~/.kube/config).
// It reports whether the config is the in-cluster ServiceAccount.
func restConfig(kubeconfig string) (*rest.Config, bool, error) {
	if kubeconfig == "" && os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		cfg, err := rest.InClusterConfig()
		return cfg, err == nil, err
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		if _, err := os.Stat(kubeconfig); err != nil {
			return nil, false, err
		}
		rules.ExplicitPath = kubeconfig
	}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{}).ClientConfig()
	if errors.Is(err, clientcmd.ErrEmptyConfig) {
		return nil, false, fmt.Errorf("no kubeconfig found and not running in a cluster: %w", err)
	}
	return cfg, false, err
}
