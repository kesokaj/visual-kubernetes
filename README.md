# visual-kubernetes

A read-only, real-time visualizer of Kubernetes internals on one white page, for live teaching demos.

## Install

One command on any cluster that grants the read-only ClusterRole (the image is public and multi-arch, amd64 + arm64):

```bash
kubectl apply -f https://github.com/kesokaj/visual-kubernetes/releases/download/v0.1.0/install.yaml
kubectl -n visual-kubernetes rollout status deploy/visual-kubernetes
kubectl -n visual-kubernetes port-forward svc/visual-kubernetes 8080:80   # then open http://localhost:8080
```

v0.1.0 is a pre-release, and GitHub's `releases/latest/download/…` URL skips pre-releases (it answers 404 until the first full release), so the command names the version.

The release asset pins the image by digest; [`deploy/install.yaml`](deploy/install.yaml) in git names the same release by tag (immutable in the registry), so copying it works too. It creates the namespace `visual-kubernetes` (Pod Security `restricted`), a ServiceAccount, a get/list/watch-only ClusterRole and its binding, one Deployment, a ClusterIP Service and a NetworkPolicy that denies all ingress. The page has no authentication: the policy keeps other pods from using its read access, and `kubectl port-forward` still works because it enters the pod's own network namespace. The policy is enforced only where the cluster's CNI implements NetworkPolicy (GKE Dataplane V2, Cilium, Calico, kind's kindnet); elsewhere any pod can reach the Service. Remove it with `kubectl delete -f` on the same file; the app keeps no state.

## Quick start (development)

Build, run and test commands are in `AGENTS.md` § Setup commands (`go run ./cmd/visual-kubernetes --replay test/fixtures/streams/deploy-scale.ndjson` shows the page without a cluster, after `pnpm -C web build`). The `GUIDE-testing` record (`fyr record get GUIDE-testing`) has the tiers behind them. Ready-to-run teaching scenarios and driver scripts are in [`examples/`](examples/README.md) (01-deployment, 02-nodes, 03-hpa, 03b-vpa).

## Architecture

One Go binary watches the cluster with client-go informers (read-only) and keeps the live model in memory. It streams changes over SSE to an embedded TanStack Start page that draws a force-directed web (SVG + d3-force; Canvas for traffic is planned). The visual reference is `design/mockup-v3.html`.

```
cmd/visual-kubernetes/  # the binary: serve (live or --replay), probe, the embedded page
internal/               # the packages, one responsibility each
web/                    # the TanStack Start frontend, built into the binary
examples/               # demo scenarios and driver scripts for teaching
test/                   # fixtures, Playwright e2e, kind integration
design/                 # approved mockup
.agents/                # project customizations and scratch/ (trail records live in fyr's brain store, read with fyr record)
```

## Using the page

| Action | Effect |
|--------|--------|
| Click a bubble | Opens the panel for it (click a pod chip in an app's panel to open that pod) |
| Esc, ×, or a click on empty space | Closes the panel; Esc also hides the replay caption |
| Wheel / drag | Zoom and pan; the camera stops auto-fitting |
| F | Fit the whole web again and resume auto-fit |
| V | Hide or show the filter pill |
| R | Replay the newest causal chain received since the page opened (1 step/s); recorded streams (`--replay`) only until live chains land (plan D48) |
| Space | Pause or resume replay |
| → | Show next step of replay (keeps pause state; opens the last chain paused if idle) |

The panel shows, per object:

- **App and pod:** a summary, metrics sparklines for the last 90 s (network rx+tx, CPU, memory, whichever the cluster reports), Events (from events.k8s.io, about the app's workloads and pods; Events about Secrets are never streamed), the pod's last 200 log lines from `/api/logs` (Refresh re-reads them), and the YAML from `/api/objects`. An app's config is its top workload (for example `apps/Deployment/shop/web`); replay mode serves only the recorded entities, so that YAML is a 404 there.
- **Node:** its conditions read from the Node YAML (falling back to its status when there are none), Events and YAML.
- **API server:** every data source and its probe state (`ok`, `missing`, `denied`, `unreachable`, `error`), the same list `visual-kubernetes probe` prints.
- **Inferred control-plane part (`cp/*`):** the evidence it was inferred from and the static pods folded behind it.

> **Warning:** logs are the application's own output and can contain secrets (tokens, passwords, personal data). Anyone who can open the page can read the last 200 lines of any pod's logs. The page never stores or forwards them, but treat a shared screen accordingly, and keep the UI behind `kubectl port-forward`.

What the web encodes:

- **Size:** an app's icon grows with its network throughput (rx + tx) when kubelet stats are readable, else with CPU from metrics.k8s.io, else with its pod count. The SVG's `data-size-by` says which measure is in use.
- **Halo:** the three busiest apps by that measure.
- **Pulse:** a ring when an app is created or changes status (a scale), or one of its pods is added, removed or changes status (for example on a restart).
- **Travel:** a pod newly placed on a node moves along that node's thread to its app before taking its place on the ring.
- **Corner counters:** what is not drawn one by one: apps and pods folded on large clusters, unreferenced ConfigMaps, and anything over the 150-bubble budget. Static control-plane pods (`kube-apiserver-*`, `etcd-*`, …) fold behind their `cp/*` part when the cluster exposes one.

## Configuration

No config file or environment settings. The binary takes flags: `--kubeconfig PATH` (local mode), `--addr HOST:PORT` (default `127.0.0.1:8080` locally, `:8080` in-cluster) and `--replay FILE` (serve a recorded stream, no cluster). A `--logs=false` flag for the LoadBalancer variant is planned (ADR-008).

## Deploy

How it runs locally and on GCP is in the `DEPLOY-local` and `DEPLOY-gcp` records; a release (image push to Artifact Registry, `gh release` with the digest-pinned `install.yaml`) goes through `/fyr-preflight`. The UI has no auth: reach it only through `kubectl port-forward`. An opt-in LoadBalancer variant limited by source ranges, with pod logs off, is planned (PRD R7, ADR-008); until it ships, never expose the Service.

## Where things are

Decisions: `fyr record list --kind adr`. What changed: `--kind change`. What went wrong and the rule that came out of it: `--kind bug`. The next session starts from the newest handoff, `fyr record list --kind handoff`.
