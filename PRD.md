# PRD — visual-kubernetes

A read-only, real-time visualizer of a Kubernetes cluster's internals on one white page, for live teaching demos.
Status: approved · Owner: kesokaj · Last updated: 2026-11-09 · Design: [SDD.md](SDD.md) · Technical design: [TDD.md](TDD.md)

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

- Changing a cluster in any way: no writes, no enabling VPA, Hubble or node pools, no provisioning.
- Helm charts or operators in v1.
- Authentication on the UI; it is reached only through `kubectl port-forward`.
- Multi-cluster views, persistence or history beyond the in-memory timeline.
- Installing optional traffic sources (Hubble, OTel); they are used only when already present.

## Must — non-negotiable

- **R1** — The app only reads: its RBAC grants get, list and watch (plus `get` on `nodes/stats`, `pods/log` and the named non-resource URLs) and no verb that writes.
  <!-- provenance: operator "Remember this is only to visualize.", 2026-11-09; enforced by: AGENTS.md § Project-Specific Standards (test planned in Phase 6: kind e2e `kubectl auth can-i --list` check) -->
- **R2** — The app never reads Secrets, except `get` on the secret `hubble-relay-client-certs` by `resourceNames`.
  <!-- provenance: grill Q8 + row 5, 2026-11-09; enforced by: not yet enforced: RBAC manifest test in deploy/ (plan Phase 6) -->
- **R3** — Installation is one `kubectl apply -f install.yaml`; any added step needs the operator's approval.
  <!-- provenance: operator "The install needs to be super easy.", 2026-11-09; enforced by: AGENTS.md § Project-Specific Standards (test planned in Phase 6: kind install e2e) -->
- **R4** — The app works on any conformant cluster that grants its RBAC, using built-in APIs first; missing optional sources degrade the view, never break it.
  <!-- provenance: operator "it should work on all clusters when having the correct permissions", "use as much built in as possible", 2026-11-09; enforced by: not yet enforced: probe unit tests per source state (plan Phase 0) -->
- **R5** — The page reflects cluster changes in real time and shows the causal chain of each observed change with pause, step and replay.
  <!-- provenance: operator turn 1 "visualizing everything in real time"; grill Q2, 2026-11-09; enforced by: not yet enforced: replay e2e (plan Phase 4) -->
- **R6** — The page is white, uses a force-directed web with official Kubernetes icons, and shows no chrome except a filter pill (all groups on by default) and a click panel.
  <!-- provenance: operator 2026-11-09 15:33 ("skip the hierarchy", "small filtering view … default is everything on"); enforced by: design/mockup-v3.html (test planned in Phase 2: Playwright filter and layout specs) -->
- **R7** — The UI is reachable only through a ClusterIP Service and `kubectl port-forward`, and docs say never to expose it.
  <!-- provenance: grill Q10 + row 12, 2026-11-09; enforced by: not yet enforced: install.yaml Service type check (plan Phase 6) -->
- **R8** — Kubelet connections always verify TLS; the app never uses `nodes/proxy` or skips verification.
  <!-- provenance: grill row 4, 2026-11-09; enforced by: not yet enforced: unit test that the kubelet client refuses an unverified cert (plan Phase 0) -->
- **R9** — The image serves every UI asset; the page makes no external network request at runtime.
  <!-- provenance: AGENTS.md UI standard, 2026-11-09; enforced by: AGENTS.md § Project-Specific Standards (test planned in Phase 2: Playwright request-origin check) -->
- **R10** — All text on the page is at least 12px.
  <!-- provenance: AGENTS.md UI standard, 2026-11-09; enforced by: not yet enforced: Playwright font-size check validated in its must-fail state (plan Phase 2) -->

## Should — expected unless a decision says otherwise

- **S1** — Installs by default into its own namespace `visual-kubernetes` with PSA `restricted`, with a kube-system variant manifest.
  <!-- provenance: grill N6, 2026-11-09; enforced by: not yet enforced: kind install e2e (plan Phase 6) -->
- **S2** — App bubbles are sized by network throughput from kubelet stats (hostNetwork pods excluded), falling back to CPU then pod count; the top 3 get a halo.
  <!-- provenance: operator "services with most data would be bigger and highlighted"; grill N2, 2026-11-09; enforced by: not yet enforced: sizing unit tests (plan Phase 1) -->
- **S3** — Traffic between pods and services is drawn from Hubble Relay when it is present, auto-detected at the Cilium and GKE DPv2 addresses.
  <!-- provenance: grill N4, 2026-11-09; enforced by: not yet enforced: hubble client unit tests (plan Phase 5) -->
- **S4** — On managed control planes the control-plane parts are inferred from Leases, `/readyz` and API server metrics and badged "inferred".
  <!-- provenance: grill row 7, 2026-11-09; enforced by: not yet enforced: model unit test (plan Phase 1) -->
- **S5** — Demo-sized clusters (≤ 20 nodes, ≤ 500 pods, ~150 bubbles) render at ≥ 30 fps; larger ones fold system namespaces and small apps into counters instead of failing.
  <!-- provenance: grill row 12, 2026-11-09; enforced by: not yet enforced: Playwright perf probe (plan Phase 2) -->
- **S6** — The repository ships `examples/` manifests and a short script for each demo: Deployment + scale, node scale-up, HPA/VPA under load, traffic with a NetworkPolicy.
  <!-- provenance: grill N5, N10, 2026-11-09; enforced by: not yet enforced: server dry-run on kind (plan Phase 4) -->
- **S7** — The same binary runs locally against a kubeconfig with no install.
  <!-- provenance: grill N7, 2026-11-09; enforced by: DEPLOY-local -->

## Could — welcome when cheap

- **C1** — OTLP receiver as a second traffic source.
- **C2** — Cloud Audit Logs stream for real (not derived) API-check steps.
- **C3** — A switch for the size metric (network, CPU, memory, pods).

## Known limits

- GKE Autopilot does not allow user workloads in kube-system — GKE docs (grill, 2026-11-09).
- Managed control planes (GKE) expose no control-plane pods — `einherjar-m0` inspection, 2026-11-09.
- Kubelet Summary API reports node-wide counters for hostNetwork pods — `einherjar-m0` `stats/summary`, 2026-11-09.
- kind cannot add worker nodes at runtime, so node scale-up is tested from a recorded event stream — grill row 10.
- The API server object-count metric is `apiserver_resource_objects` on 1.36 — `einherjar-m0` `/metrics`, 2026-11-09.

## Open questions

- ~~Pod logs in the click panel need `get pods/log`, which grill Q8 excluded~~ — struck 2026-11-09: granted (plan D9).
- Exposing the UI through a network LoadBalancer on a test cluster (operator, 2026-11-09 19:05) conflicts with R7 — decided by a PRD edit proposal in the next session.
