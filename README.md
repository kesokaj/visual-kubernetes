# visual-kubernetes

A real-time visualizer of Kubernetes internals on one white page, built for live teaching demos on non-production clusters (read-only by default; opt-in `--demo` mode for interactive demo clusters).

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

| Manifest | Mode & exposure | Use when |
|----------|-----------------|----------|
| [`deploy/install.yaml`](deploy/install.yaml) | Read-only · own namespace `visual-kubernetes` (`restricted` PSA) · `ClusterIP` (`kubectl port-forward`) + deny-all-ingress `NetworkPolicy` | Default on any Kubernetes cluster (including GKE Autopilot) |
| [`deploy/install-kube-system.yaml`](deploy/install-kube-system.yaml) | Read-only · `kube-system` · `ClusterIP` (`kubectl -n kube-system port-forward`) + pod-scoped deny-all-ingress `NetworkPolicy` (no `Namespace` object) | Clusters where you prefer `kube-system` over a dedicated namespace (not GKE Autopilot) |
| [`deploy/install-lb.yaml`](deploy/install-lb.yaml) | Read-only (`--logs=false`) · `LoadBalancer` (`externalTrafficPolicy: Local`) + `loadBalancerSourceRanges` + matching `ipBlock` `NetworkPolicy` | Shared demo cluster behind client CIDR allowlists (`192.0.2.0/32` placeholder fails closed until replaced) |
| [`deploy/install-demo.yaml`](deploy/install-demo.yaml) | Interactive `--demo` (**non-production only**, ADR-010/ADR-011) · `ClusterIP` (`kubectl port-forward`) + deny-all-ingress `NetworkPolicy` | Local or port-forwarded teaching demos needing in-app Service proxy, workload `env` editing, and pod `exec` (`localhost` `Host` required) |
| [`deploy/install-iap.yaml`](deploy/install-iap.yaml) | Interactive `--demo` + Google IAP (`--iap-audience` + `--allowed-host`, **non-production only**, ADR-011) · `ClusterIP` (GKE NEG) + Google LB/IAP `ipBlock` `NetworkPolicy` | GKE teaching cluster behind an External HTTPS Application Load Balancer + Identity-Aware Proxy |

- **Replacing fail-closed placeholders:** On `install-lb.yaml`, replace `192.0.2.0/32` with your client CIDR(s) (`sed -i.bak 's|192.0.2.0/32|<YOUR_IP>/32|g' install-lb.yaml`). On `install-iap.yaml`, replace `000000000000` / `0000000000000000` in `--iap-audience` with your numeric GCP Project Number and BackendService ID and `visual-kubernetes.example.internal` in `--allowed-host` with your HTTPS Load Balancer hostname.
- **Installing a local source build onto `kind`:** Before a new release tag is published, build and side-load `visual-kubernetes:dev` onto your local `kind` cluster:
  ```bash
  pnpm -C web build && docker build -t visual-kubernetes:dev .
  kind load docker-image visual-kubernetes:dev --name visual-kubernetes
  kubectl apply -f deploy/install.yaml   # or deploy/install-demo.yaml on a non-production demo cluster
  kubectl -n visual-kubernetes set image deploy/visual-kubernetes visual-kubernetes=visual-kubernetes:dev
  ```

### Uninstall

Every manifest uses only stateless core Kubernetes objects (`Namespace`, `ServiceAccount`, `ClusterRole`, `ClusterRoleBinding`, `Deployment`, `Service`, `NetworkPolicy`) with `app.kubernetes.io/name: visual-kubernetes` and zero finalizers, PVCs, CRDs, or webhooks. Remove an install in one command with the same manifest you applied:

```bash
kubectl delete --ignore-not-found -f https://github.com/kesokaj/visual-kubernetes/releases/download/v0.1.0/install.yaml
# or from a local checkout (install.yaml, install-kube-system.yaml, install-lb.yaml, install-demo.yaml, install-iap.yaml):
kubectl delete --ignore-not-found -f deploy/install.yaml
```

When switching between variants (for example `install-lb.yaml` or `install-demo.yaml` back to `install.yaml`), run `kubectl delete --ignore-not-found -f <previous-manifest>` first so wider RBAC rules or additive `NetworkPolicy` ingress rules are removed cleanly.

> [!WARNING]
> **LoadBalancer exposure (`install-lb.yaml`):** The page has **no authentication**. Anyone inside `loadBalancerSourceRanges` can read every watched object in the cluster (Secrets are excluded; the pod's single named `hubble-relay-client-certs` Secret read is never served by `/api/objects`). Setting `0.0.0.0/0` (or `::/0`) makes the cluster's objects **public** to the internet. Source ranges hold only where the cluster's LoadBalancer implementation enforces `loadBalancerSourceRanges`, and `visual-kubernetes-lb-ingress` restricts TCP `8080` to the same `ipBlock` CIDR(s) using `externalTrafficPolicy: Local` so in-cluster pods cannot bypass the LoadBalancer ranges where the CNI enforces `NetworkPolicy` (if your cloud LoadBalancer SNATs client IPs or probes pod port `8080` directly instead of `healthCheckNodePort`, add its proxy/health-check CIDR under `spec.ingress[0].from`). Internal-only LoadBalancers require cloud-specific Service annotations (such as `networking.gke.io/load-balancer-type: "Internal"` on GKE), which are the operator's own edit before applying.

## Quick start (development)

Build, run and test commands are in `AGENTS.md` § Setup commands (`go run ./cmd/visual-kubernetes --replay test/fixtures/streams/deploy-scale.ndjson` shows the page without a cluster, after `pnpm -C web build`). The `GUIDE-testing` record (`fyr record get GUIDE-testing`) has the tiers behind them. Ready-to-run teaching scenarios and driver scripts are in [`examples/`](examples/README.md) (01-deployment, 02-nodes, 03-hpa, 03b-vpa, 04-traffic).

## Architecture

One Go binary watches the cluster with client-go informers (read-only by default; opt-in `--demo` for non-production interactive demos) and keeps the live model in memory. It streams changes over SSE to an embedded TanStack Start page that draws a force-directed web (SVG + d3-force, with a Canvas 2D particle overlay for traffic flows). The visual reference is `design/mockup-v3.html`.

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

<details>
<summary>Page controls and keyboard shortcuts (14 actions)</summary>

| Action | Effect |
|--------|--------|
| Click a bubble | Opens the panel for it (click a pod chip in an app's panel to open that pod); everything outside its neighbourhood fades until the panel closes |
| Hover a bubble | Fades everything outside its neighbourhood (its app's pods and satellites, and the bodies it links to) while the pointer stays on it |
| Esc, ×, or a click on empty space | Closes the panel; Esc also hides the replay caption |
| Drag a bubble | Moves it; the rest of the web rearranges around it (the API hub stays pinned) and auto-fit stops |
| Wheel, pinch / drag on empty space | Zoom and pan; the camera stops auto-fitting |
| `+` / `−` (keys or the buttons in the filter pill) | Zoom in or out around the centre |
| Double-click | On a bubble: zoom onto it; on empty space: zoom in there |
| F, or Fit in the filter pill | Fit the whole web again and resume auto-fit |
| Filter pill (bottom-left) | Toggle the 8 component groups (all on), show `visual-kubernetes` itself (hidden by default), `Highlight` / `Only show` matching `Deployment`, `StatefulSet`, `DaemonSet` and `Namespace` items, or switch what sizes apps (`Size by`: `Auto`, `Network`, `CPU`, `Memory`, `Pods`) |
| V | Hide or show the filter pill |
| R | Replay the newest causal chain received since the page opened (1 step/s) |
| Space | Pause or resume replay |
| → | Show next step of replay (keeps pause state; opens the last chain paused if idle) |

</details>

The panel shows, per object:

- **App and pod:** a summary, metrics sparklines for the last 90 s (network rx+tx, CPU, memory, whichever the cluster reports), Events (from events.k8s.io, about the app's workloads and pods; Events about Secrets are never streamed), the pod's last 200 log lines from `/api/logs` (Refresh re-reads them; when started with `--logs=false`, `/api/logs` answers `403` and the panel shows `"Pod logs are off on this install."`), and the YAML from `/api/objects`. An app's config is its top workload (for example `apps/Deployment/shop/web`); replay mode serves only the recorded entities, so that YAML is a 404 there.
- **Node:** its conditions read from the Node YAML (falling back to its status when there are none), Events and YAML.
- **API server:** every data source and its probe state (`ok`, `missing`, `denied`, `unreachable`, `error`), the same list `visual-kubernetes probe` prints.
- **Inferred control-plane part (`cp/*`):** the evidence it was inferred from and the static pods folded behind it.
- **Internet (globe, `ext/internet`):** drawn only when the cluster has an entrypoint (an Ingress, Gateway, LoadBalancer or NodePort Service); the panel lists those entrypoints. Hubble `reserved:world` flows and Prometheus flows with an external side draw particles from or to the globe.
- **Interactive demo proxy (`--demo`, non-production only):** on exposed non-system apps/Services (and `ext/internet`), the panel adds an **Exposed ports · demo proxy** gatekeeper. Every port starts closed (`403`); clicking **Open in app** mints a revocable token and opens the Service inside an on-demand sandboxed in-app browser sheet (`<iframe sandbox="allow-scripts allow-forms">`), and **Close port** revokes it immediately.
- **Interactive demo env editor (`--demo`, non-production only):** on non-system `Deployment`, `StatefulSet`, and `DaemonSet` apps, the panel adds an **Environment · demo editor** section to add, edit, or remove plain container `env` variables with `resourceVersion` `409` conflict detection while keeping `valueFrom` (`secretKeyRef`, `configMapKeyRef`, `fieldRef`) entries locked read-only.
- **Interactive demo pod terminal (`--demo`, non-production only):** on running non-system `Pod` and app panels, **Pod terminal** opens an embedded `@xterm/xterm` (`13px`) drawer connected over `/api/demo/exec` (`/bin/sh` or `/bin/bash`, container tabs on multi-container pods, max 4 concurrent sessions, and a clear notice on distroless containers without a shell; `Esc` or **Close** dismisses it).

> [!WARNING]
> Logs are the application's own output and can contain secrets (tokens, passwords, personal data). On installs with `--logs=true` (the default in `install.yaml`, `install-kube-system.yaml`, `install-demo.yaml`, and `install-iap.yaml`), anyone who can open the page can read the last 200 lines of any pod's logs: treat a shared screen accordingly, keep ClusterIP installs behind `kubectl port-forward` or Google IAP, and leave `--logs=false` enabled on `install-lb.yaml`.

What the web encodes:

- **Size:** by default (`Size by: Auto`) an app's icon grows with its network throughput (rx + tx) when kubelet stats are readable, else with CPU from metrics.k8s.io, else with its pod count. The filter pill's `Size by` switch pins `Network`, `CPU`, `Memory` (metrics.k8s.io working set) or `Pods` instead; metrics the cluster does not report are greyed out, and the pill label says `size: <metric>` while one is pinned. The SVG's `data-size-by` says which measure is in use.
- **Halo:** the three busiest apps by that measure.
- **Pulse:** a ring when an app is created or changes status (a scale), or one of its pods is added, removed or changes status (for example on a restart).
- **Travel:** a pod newly placed on a node moves along that node's thread to its app before taking its place on the ring.
- **Traffic:** when Hubble Relay (Cilium or GKE Dataplane V2) or an in-cluster Prometheus flow series is available, curved edges carry blue particles for forwarded flows and red particles plus `×` marks for `NetworkPolicy` drops. Traffic addressed to a Service rides through that Service's bubble or satellite on its way to the app (from Hubble's `destination_service`, or inferred from the app's only Service or the one whose target port matches, so it works under kube-proxy too).
- **Hover:** pointing at any object shows its name and what it is (`Kind · namespace`).
- **Corner counters:** what is not drawn one by one: apps and pods folded on large clusters, unreferenced ConfigMaps, and anything over the 150-bubble budget. Static control-plane pods (`kube-apiserver-*`, `etcd-*`, …) fold behind their `cp/*` part when the cluster exposes one.

## Configuration

No config file or environment settings. The binary takes flags: `--kubeconfig PATH` (local mode), `--addr HOST:PORT` (default `127.0.0.1:8080` locally, `:8080` in-cluster), `--replay FILE` (serve a recorded stream, no cluster), `--logs` (default `true`; `--logs=false` makes `/api/logs` answer `403` and the panel say `"Pod logs are off on this install."`, as `deploy/install-lb.yaml` sets), `--demo` (default `false`; enables `/api/demo/*` for **non-production demo clusters only**, requiring loopback `Host` headers unless `--iap-audience` is set), `--allowed-host HOSTS` (comma-separated DNS `Host` names permitted alongside `localhost` and IP literals), and `--iap-audience AUD` (Google Identity-Aware Proxy audience `/projects/<PROJECT_NUMBER>/...` verified via `google.golang.org/api/idtoken` on every UI and `/api/*` route, requiring `--allowed-host`).

## Deploy

How it runs locally and on GCP is in the `DEPLOY-local` and `DEPLOY-gcp` records; a release (image push to Artifact Registry, `gh release` with the digest-pinned `install.yaml`, `install-kube-system.yaml`, `install-lb.yaml`, `install-demo.yaml`, and `install-iap.yaml`) goes through `/fyr-preflight`. Default installs have no auth: use `kubectl port-forward` by default, narrow client CIDRs in `loadBalancerSourceRanges` on read-only `install-lb.yaml` (PRD R7, ADR-008), or Google IAP (`install-iap.yaml`, ADR-011) when exposing `--demo` over HTTPS on GKE.

## Where things are

Decisions: `fyr record list --kind adr`. What changed: `--kind change`. What went wrong and the rule that came out of it: `--kind bug`. The next session starts from the newest handoff, `fyr record list --kind handoff`.
