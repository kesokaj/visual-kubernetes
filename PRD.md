# PRD — visual-kubernetes

A real-time visualizer of a Kubernetes cluster's internals on one white page for live teaching demos — read-only by default, with an opt-in interactive demo mode for non-production demo clusters.
Status: approved · Owner: kesokaj · Last updated: 2026-11-12 · Design: [SDD.md](SDD.md) · Technical design: [TDD.md](TDD.md)

<!-- Read before every significant change. Immutable without the user's explicit approval of the exact text:
     propose the edit → approval → ADR → PRD.md and TODO.md in one commit (global context § Hard rules).
     Nothing here needs routine edits: a goal's baseline is a dated starting point, never updated; `Last updated`
     is set by the commit that applies an approved edit; how each requirement is met and tested is traced in
     SDD.md and TDD.md, which agents keep current. The `requirements` skill elicits the items below and traces
     them; it never writes "approved" itself. -->

## Problem

People learning Kubernetes cannot see what the cluster does when they apply a Deployment, scale, add nodes or send traffic: `kubectl` shows text after the fact, and the control loops stay invisible. The operator teaches live demos and needs one page that shows these internals as they happen, on whichever cluster the demo runs.

## Users

- **Presenter:** runs a live teaching demo; needs a one-command install, a page that reacts within seconds, and a way to replay what just happened.
- **Audience:** watches the shared screen; needs large, readable, uncluttered visuals (white page, text ≥ 12px).
- **Cluster admin:** grants the RBAC; needs proof the app is read-only and holds no Secrets.

## Goals

| # | Goal | Metric | Baseline (2026-11-09) | Target |
|---|---|---|---|---|
| G1 | Install is trivial | commands from a fresh cluster to the page open | no product | 2 (`kubectl apply`, `kubectl port-forward`) |
| G2 | Changes show live | time from an object change in the API to its pulse on the page | no product | ≤ 2 s p95 on kind |
| G3 | Demo-sized clusters render smoothly | frame rate at 150 bubbles / 1,000 pod dots | no product | ≥ 30 fps in the Playwright perf probe |

## Non-goals

- Production cluster operation or general cluster administration: interactive demo mode (`--demo`, `install-demo.yaml`, `install-iap.yaml`) is strictly for non-production teaching demos; the app never provisions clusters or enables cluster features (VPA, Hubble, node pools), and never mutates or enters system namespaces.
- Helm charts or operators in v1.
- Built-in user management or login forms. Default installs use `kubectl port-forward`; `install-lb.yaml` stays read-only behind source ranges; shared remote access to interactive demo mode uses Google Identity-Aware Proxy (`--iap-audience`, `install-iap.yaml`, R7).
- Multi-cluster views, persistence or history beyond the in-memory timeline.
- Installing optional traffic sources (Hubble, OTel); they are used only when already present.

## Must — non-negotiable

- **R1** — By default (`install.yaml`, `install-kube-system.yaml`, `install-lb.yaml`, and `--demo=false`), the app only reads: its RBAC grants get, list and watch (plus `get` on `nodes/stats`, `pods/log` and the named non-resource URLs) and no verb that writes or enters pods. Opt-in interactive demo mode (`--demo`, `install-demo.yaml`, `install-iap.yaml` — non-production demo clusters only) adds `patch` and `update` on `apps` `deployments`, `statefulsets` and `daemonsets` (plain container `env` only) plus `get` and `create` on `pods/exec`, refuses system namespaces (`kube-system`, `kube-public`, `kube-node-lease`, `visual-kubernetes`, `gke-managed-*`, `gmp-*`), and proxies only presenter-opened Service ports.
  <!-- provenance: operator "Remember this is only to visualize.", 2026-11-09, amended 2026-11-12 (ADR-010); enforced by: `deploy/install_test.go` TestInstallManifestReadOnly, `test/e2e/live.spec.ts` validateCanIList (each validated in its must-fail state) -->
- **R2** — The app never reads Secrets, except `get` on the secret `hubble-relay-client-certs` by `resourceNames`.
  <!-- provenance: grill Q8 + row 5, 2026-11-09; enforced by: `deploy/install_test.go` TestInstallManifestReadOnly, `internal/watch/watch_test.go` TestWatcherNeverReadsSecrets, `internal/model/satellites_test.go` TestSecretsAndEventsAreNeverStored -->
- **R3** — Installation is one `kubectl apply -f install.yaml`; any added step needs the operator's approval.
  <!-- provenance: operator "The install needs to be super easy.", 2026-11-09; enforced by: `deploy/install.yaml`, `test/kind/install-check.sh`, `test/e2e/live.sh` -->
- **R4** — The app works on any conformant cluster that grants its RBAC, using built-in APIs first; missing optional sources degrade the view, never break it.
  <!-- provenance: operator "it should work on all clusters when having the correct permissions", "use as much built in as possible", 2026-11-09; enforced by: `internal/probe/probe_test.go` TestProbeStates, `internal/watch/watch_test.go` TestWatcherSyncsAroundForbiddenGroup, `test/e2e/live.spec.ts` -->
- **R5** — The page reflects cluster changes in real time and shows the causal chain of each observed change with pause, step and replay.
  <!-- provenance: operator turn 1 "visualizing everything in real time"; grill Q2, 2026-11-09; enforced by: `cmd/visual-kubernetes/serve_test.go` TestChainSinkPrimesThenObserves, `test/e2e/replay.spec.ts`, `test/e2e/live.spec.ts` -->
- **R6** — The page is white with a subtle grid-paper background, uses an airy force-directed web with official Kubernetes icons, hides `visual-kubernetes` itself by default, and shows no standing chrome except a compact filter pill (8 component groups on by default, a `visual-kubernetes` toggle, and `Deployment`/`StatefulSet`/`DaemonSet` and `Namespace` highlight-or-only-show filters) and the click panel (which in `--demo` mode may open an on-demand sandboxed Service browser sheet or pod terminal drawer).
  <!-- provenance: operator 2026-11-09 15:33 ("skip the hierarchy", "small filtering view … default is everything on"), amended 2026-11-11 (D54) and 2026-11-12 (ADR-010); enforced by: `test/e2e/web.spec.ts`, `test/e2e/filter.spec.ts`, `test/e2e/panel.spec.ts` -->
- **R7** — The default `install.yaml` and opt-in `install-demo.yaml` expose the UI only through a ClusterIP Service and `kubectl port-forward`; without `--iap-audience`, `--demo` refuses non-loopback `Host` headers on every non-`/healthz` route. `install-lb.yaml` stays read-only (`--logs=false`, `--demo=false`) behind operator-set `loadBalancerSourceRanges`. Shared remote access to `--demo` requires `install-iap.yaml` (`--iap-audience` + `--allowed-host`), validating `x-goog-iap-jwt-assertion` via `google.golang.org/api/idtoken` on every UI and `/api/*` request.
  <!-- provenance: operator "loadbalancer yes", 2026-11-10 (ADR-008), amended 2026-11-12 (ADR-011); enforced by: `deploy/install_test.go` TestInstallServiceIsClusterIP, `deploy/variants_test.go` TestInstallLBRequiresSourceRanges, `cmd/visual-kubernetes/serve_test.go` TestRunLogsFlag, `test/e2e/panel.spec.ts` -->
- **R8** — Kubelet connections always verify TLS; the app never uses `nodes/proxy` or skips verification.
  <!-- provenance: grill row 4, 2026-11-09; enforced by: `internal/kubelet/client_test.go` TestClientRefusesUnverifiedCert (fails when verification is disabled; ADR-005) -->
- **R9** — The image serves every UI asset; the page makes no external network request at runtime.
  <!-- provenance: AGENTS.md UI standard, 2026-11-09; enforced by: `test/e2e/checks.ts` foreignRequests (validated in its must-fail state by test/e2e/web.spec.ts) -->
- **R10** — All text on the page is at least 12px.
  <!-- provenance: AGENTS.md UI standard, 2026-11-09; enforced by: `test/e2e/checks.ts` smallText (validated in its must-fail state by test/e2e/web.spec.ts) -->

## Should — expected unless a decision says otherwise

- **S1** — Installs by default into its own namespace `visual-kubernetes` with PSA `restricted`, with a kube-system variant manifest.
  <!-- provenance: grill N6, 2026-11-09; enforced by: `deploy/install_test.go` TestInstallPodIsRestricted, `deploy/variants_test.go` TestInstallKubeSystemVariant, `test/kind/install-check.sh` -->
- **S2** — App bubbles are sized by network throughput from kubelet stats (hostNetwork pods excluded), falling back to CPU then pod count; the top 3 get a halo.
  <!-- provenance: operator "services with most data would be bigger and highlighted"; grill N2, 2026-11-09; enforced by: `internal/sources/sources_test.go` TestHostNetworkPodsExcluded, `internal/sources/sources_test.go` TestSizeFallback, `test/e2e/encodings.spec.ts` -->
- **S3** — Traffic between pods and services is drawn from Hubble Relay when it is present, auto-detected at the Cilium and GKE DPv2 addresses.
  <!-- provenance: grill N4, 2026-11-09; enforced by: `internal/hubble/client_test.go` TestAutoDetectCiliumPlaintextAggregatesFlows, `internal/hubble/client_test.go` TestAutoDetectGKEDPv2mTLS, `test/e2e/traffic.spec.ts` -->
- **S4** — On managed control planes the control-plane parts are inferred from Leases, `/readyz` and API server metrics and badged "inferred".
  <!-- provenance: grill row 7, 2026-11-09; enforced by: `internal/sources/controlplane_test.go` TestControlPlaneInference, `internal/sources/controlplane_test.go` TestControlPlaneManaged, `test/e2e/panel.spec.ts` -->
- **S5** — Demo-sized clusters (≤ 20 nodes, ≤ 500 pods, ~150 bubbles) render at ≥ 30 fps; larger ones fold system namespaces and small apps into counters instead of failing.
  <!-- provenance: grill row 12, 2026-11-09; enforced by: `internal/model/fold_test.go` TestFoldingBeyondS5Limits, `test/e2e/folding.spec.ts`, `test/e2e/perf.spec.ts` -->
- **S6** — The repository ships `examples/` manifests and a short script for each demo: Deployment + scale, node scale-up, HPA/VPA under load, traffic with a NetworkPolicy.
  <!-- provenance: grill N5, N10, 2026-11-09; enforced by: `examples/README.md`, `test/e2e/live.spec.ts` -->
- **S7** — The same binary runs locally against a kubeconfig with no install.
  <!-- provenance: grill N7, 2026-11-09; enforced by: DEPLOY-local -->

## Could — welcome when cheap

- **C1** — OTLP receiver as a second traffic source.
- **C2** — Cloud Audit Logs stream for real (not derived) API-check steps.
- **C3** — A switch for the size metric (network, CPU, memory, pods).
  <!-- provenance: grill, 2026-11-09; built 2026-11-12 (filter pill `Size by`, default Auto keeps S2); enforced by: `test/e2e/size.spec.ts` (validated in its must-fail state) -->

## Known limits

- GKE Autopilot does not allow user workloads in kube-system — GKE docs (grill, 2026-11-09).
- Managed control planes (GKE) expose no control-plane pods — `einherjar-m0` inspection, 2026-11-09.
- Kubelet Summary API reports node-wide counters for hostNetwork pods — `einherjar-m0` `stats/summary`, 2026-11-09.
- kind cannot add worker nodes at runtime, so node scale-up is tested from a recorded event stream — grill row 10.
- The API server object-count metric is `apiserver_resource_objects` on 1.36 — `einherjar-m0` `/metrics`, 2026-11-09.

## Open questions

- ~~Pod logs in the click panel need `get pods/log`, which grill Q8 excluded~~ — struck 2026-11-09: granted (plan D9).
