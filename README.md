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

### Install variants

- **`kube-system` variant ([`deploy/install-kube-system.yaml`](deploy/install-kube-system.yaml)):** places the ServiceAccount, Deployment, ClusterIP Service and a pod-scoped deny-all-ingress NetworkPolicy (`app.kubernetes.io/name: visual-kubernetes`) in `kube-system` without touching the `kube-system` Namespace object (note GKE Autopilot forbids user workloads in `kube-system`). Reach it with `kubectl -n kube-system port-forward svc/visual-kubernetes 8080:80`.
- **Opt-in LoadBalancer variant ([`deploy/install-lb.yaml`](deploy/install-lb.yaml)):** exposes the UI on a network `LoadBalancer` Service (`externalTrafficPolicy: Local`) with pod logs disabled (`--logs=false`), a fail-closed placeholder `loadBalancerSourceRanges` (`192.0.2.0/32`, RFC 5737 `TEST-NET-1`), and a matching `ipBlock` ingress `NetworkPolicy`. Download the manifest, replace `192.0.2.0/32` with your own client CIDR(s) (`sed -i.bak 's|192.0.2.0/32|<YOUR_IP>/32|g' install-lb.yaml`), and apply it. When switching back to `install.yaml`, run `kubectl delete -f install-lb.yaml` (or delete `networkpolicy/visual-kubernetes-lb-ingress`) first, because Kubernetes `NetworkPolicy` ingress rules are additive across policies in the same namespace.

> [!WARNING]
> **LoadBalancer exposure (`install-lb.yaml`):** The page has **no authentication**. Anyone inside `loadBalancerSourceRanges` can read every watched object in the cluster (Secrets are excluded; the pod's single named `hubble-relay-client-certs` Secret read is never served by `/api/objects`). Setting `0.0.0.0/0` (or `::/0`) makes the cluster's objects **public** to the internet. Source ranges hold only where the cluster's LoadBalancer implementation enforces `loadBalancerSourceRanges`, and `visual-kubernetes-lb-ingress` restricts TCP `8080` to the same `ipBlock` CIDR(s) using `externalTrafficPolicy: Local` so in-cluster pods cannot bypass the LoadBalancer ranges where the CNI enforces `NetworkPolicy` (if your cloud LoadBalancer SNATs client IPs or probes pod port `8080` directly instead of `healthCheckNodePort`, add its proxy/health-check CIDR under `spec.ingress[0].from`). Internal-only LoadBalancers require cloud-specific Service annotations (such as `networking.gke.io/load-balancer-type: "Internal"` on GKE), which are the operator's own edit before applying.

## Quick start (development)

Build, run and test commands are in `AGENTS.md` § Setup commands (`go run ./cmd/visual-kubernetes --replay test/fixtures/streams/deploy-scale.ndjson` shows the page without a cluster, after `pnpm -C web build`). The `GUIDE-testing` record (`fyr record get GUIDE-testing`) has the tiers behind them. Ready-to-run teaching scenarios and driver scripts are in [`examples/`](examples/README.md) (01-deployment, 02-nodes, 03-hpa, 03b-vpa, 04-traffic).

## Architecture

One Go binary watches the cluster with client-go informers (read-only) and keeps the live model in memory. It streams changes over SSE to an embedded TanStack Start page that draws a force-directed web (SVG + d3-force, with a Canvas 2D particle overlay for traffic flows). The visual reference is `design/mockup-v3.html`.

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

Everything on the page is driven by direct clicks, pointer pan/zoom, a compact bottom-left filter menu, and single-key presenter shortcuts.

| Action | Effect |
|--------|--------|
| Click a bubble | Opens the panel for it (click a pod chip in an app's panel to open that pod) |
| Esc, ×, or a click on empty space | Closes the panel; Esc also hides the replay caption |
| Wheel / drag | Zoom and pan; the camera stops auto-fitting |
| F | Fit the whole web again and resume auto-fit |
| Filter pill (bottom-left) | Toggle the 8 component groups (all on), show `visual-kubernetes` itself (hidden by default), or `Highlight` / `Only show` matching `Deployment`, `StatefulSet`, `DaemonSet` and `Namespace` items |
| V | Hide or show the filter pill |
| R | Replay the newest causal chain received since the page opened (1 step/s) |
| Space | Pause or resume replay |
| → | Show next step of replay (keeps pause state; opens the last chain paused if idle) |

The panel shows, per object:

- **App and pod:** a summary, metrics sparklines for the last 90 s (network rx+tx, CPU, memory, whichever the cluster reports), Events (from events.k8s.io, about the app's workloads and pods; Events about Secrets are never streamed), the pod's last 200 log lines from `/api/logs` (Refresh re-reads them; when started with `--logs=false`, `/api/logs` answers `403` and the panel shows `"Pod logs are off on this install."`), and the YAML from `/api/objects`. An app's config is its top workload (for example `apps/Deployment/shop/web`); replay mode serves only the recorded entities, so that YAML is a 404 there.
- **Node:** its conditions read from the Node YAML (falling back to its status when there are none), Events and YAML.
- **API server:** every data source and its probe state (`ok`, `missing`, `denied`, `unreachable`, `error`), the same list `visual-kubernetes probe` prints.
- **Inferred control-plane part (`cp/*`):** the evidence it was inferred from and the static pods folded behind it.

> [!WARNING]
> Logs are the application's own output and can contain secrets (tokens, passwords, personal data). On installs with `--logs=true` (the default in `install.yaml` and `install-kube-system.yaml`), anyone who can open the page can read the last 200 lines of any pod's logs: treat a shared screen accordingly, keep ClusterIP installs behind `kubectl port-forward`, and leave `--logs=false` enabled on `install-lb.yaml`.

What the web encodes:

- **Size:** an app's icon grows with its network throughput (rx + tx) when kubelet stats are readable, else with CPU from metrics.k8s.io, else with its pod count. The SVG's `data-size-by` says which measure is in use.
- **Halo:** the three busiest apps by that measure.
- **Pulse:** a ring when an app is created or changes status (a scale), or one of its pods is added, removed or changes status (for example on a restart).
- **Travel:** a pod newly placed on a node moves along that node's thread to its app before taking its place on the ring.
- **Traffic:** when Hubble Relay (Cilium or GKE Dataplane V2) or an in-cluster Prometheus flow series is available, curved edges carry blue particles for forwarded flows and red particles plus `×` marks for `NetworkPolicy` drops.
- **Corner counters:** what is not drawn one by one: apps and pods folded on large clusters, unreferenced ConfigMaps, and anything over the 150-bubble budget. Static control-plane pods (`kube-apiserver-*`, `etcd-*`, …) fold behind their `cp/*` part when the cluster exposes one.

## Configuration

No config file or environment settings. The binary takes flags: `--kubeconfig PATH` (local mode), `--addr HOST:PORT` (default `127.0.0.1:8080` locally, `:8080` in-cluster), `--replay FILE` (serve a recorded stream, no cluster) and `--logs` (default `true`; `--logs=false` makes `/api/logs` answer `403` and the panel say `"Pod logs are off on this install."`, as `deploy/install-lb.yaml` sets).

## Deploy

How it runs locally and on GCP is in the `DEPLOY-local` and `DEPLOY-gcp` records; a release (image push to Artifact Registry, `gh release` with the digest-pinned `install.yaml`, `install-kube-system.yaml` and `install-lb.yaml`) goes through `/fyr-preflight`. The UI has no auth: use `kubectl port-forward` by default, or set narrow client CIDRs in `loadBalancerSourceRanges` when opting into `install-lb.yaml` (PRD R7, ADR-008).

## Where things are

Decisions: `fyr record list --kind adr`. What changed: `--kind change`. What went wrong and the rule that came out of it: `--kind bug`. The next session starts from the newest handoff, `fyr record list --kind handoff`.
