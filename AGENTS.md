# AGENTS.md

## Project Overview

`visual-kubernetes` is a read-only, real-time visualizer of a Kubernetes cluster's internals on one white page, built for live teaching demos. Presenters use it to show what happens when someone adds a Deployment, scales with HPA/VPA, adds worker nodes, or sends traffic between services. It installs as one Deployment named `visual-kubernetes`. It installs by default into its own namespace `visual-kubernetes`, with a `kube-system` variant manifest (PRD S1; GKE Autopilot forbids workloads in `kube-system`). It works on any conformant cluster that grants its read-only RBAC. It uses built-in sources first (watches, Events, Leases, metrics.k8s.io, kubelet stats, API server `/metrics`). Optional traffic sources (Hubble Relay, OTel) are auto-detected, never installed. The app never changes a cluster.

## Setup Commands

One Go module (`github.com/kesokaj/visual-kubernetes`, go 1.27, client-go v0.37) plus one pnpm package in `web/` (TanStack Start SPA, Tailwind, d3-force). The binary embeds the page built into `cmd/visual-kubernetes/ui/dist` and serves it at `/` beside the live model over SSE (`/api/stream`, `/api/objects`, `/api/logs`, `/healthz`). It also has a `probe` subcommand. Without a page build, `/` answers with a "page not built" note.

```bash
corepack enable pnpm                     # once; Node ≥ 22 (Node 26 needs: npm i -g corepack)
pnpm -C web install
pnpm -C web build && go build ./...      # build the page first: go:embed picks it up at compile time
go vet ./... && go test -race ./...
pnpm -C web exec playwright install --only-shell chromium   # once
pnpm -C web exec playwright test         # e2e: runs the binary with --replay on 127.0.0.1:18080–18087
test/kind/up.sh        # kind cluster on Colima; kubeconfig in .agents/scratch/kind/kubeconfig
go run ./cmd/visual-kubernetes probe --kubeconfig .agents/scratch/kind/kubeconfig
go run ./cmd/visual-kubernetes --kubeconfig .agents/scratch/kind/kubeconfig   # serve on 127.0.0.1:8080
go run ./cmd/visual-kubernetes --replay test/fixtures/streams/deploy-scale.ndjson   # no cluster
test/kind/probe.sh     # image build + kind load + in-cluster probe
KUBECONFIG=.agents/scratch/kind/kubeconfig VK_MUST_FAIL=1 test/kind/install-check.sh [MANIFEST]   # live install check: rollout, healthz/page/stream, can-i, ingress denied both ways
go run ./test/fixturegen   # rewrite test/fixtures/streams/*.ndjson after a model change (TestFixturesAreCurrent fails when stale)
```

`visual-kubernetes probe` prints `<source> ok|missing|denied|unreachable|error <detail>` for discovery, leases, events, endpointslices, metrics.k8s.io, kubelet, apiserver-metrics, readyz and hubble-relay, and exits 0 whatever the sources report (1 only when no client can be built, 2 on usage). Always pass `--kubeconfig` locally: without it the standard loading rules pick the operator's current context.

## Architecture

The agreed shape is in `SDD.md` and the open plan record PLAN-2026-11-09-1906-plan-visual-kubernetes-v1-live-spider-web-visual. The approved visual reference is `design/mockup-v3.html`: a force-directed "spider web" on white, apps as bubbles with pods hanging around them, official Kubernetes icons, a small filter pill (all on by default), and click for config, logs and metrics. ADRs: `fyr record list --kind adr`.

The page (`web/src/`) keeps the stream in a `Graph` store (`lib/graph.ts`, events parsed by `lib/wire.ts`), with metrics history and Events in `lib/telemetry.ts` and pulses and pod travel in `lib/motion.ts`. Causal chains arrive as `chain` events (`lib/chains.ts` keeps the newest copy per id, max 16). In `web/src/web/`, `scene.ts` turns the graph into anchors and links for the groups the filter shows. `folding.ts` folds static control-plane pods behind their `cp/*` part (D30), and `sizing.ts` sizes apps by network, then CPU, then pods, with a halo on the top 3. `layout.ts` runs d3-force over those, with the API hub pinned at the centre. `camera.ts` auto-fits the web at 16:9 until the presenter zooms or pans; F fits again and V hides the pill. At most 150 bubbles are drawn, and `Counters.tsx` shows the rest as corner counters (D33). Labels are drawn in screen space so they stay 14/12px at any zoom. The click panel (`web/src/panel/`) shows config (`/api/objects`), Events, metrics sparklines, pod logs (`/api/logs`), node conditions and the API server's data sources. Teaching replay (`web/src/replay/`: `player.ts`, `useReplay.ts`, `Caption.tsx`, `target.ts`, `replay.css`) replays the newest chain received since open at 1 step/s with R; Space pauses/resumes, → steps, and Esc closes the panel and hides the caption. The caption points at the API hub for derived steps, else the object's app (text 12–16px, D50).

PRD R7 (ADR-008): the default install is `deploy/install.yaml`: a ClusterIP Service reached through `kubectl port-forward`, plus a NetworkPolicy that denies all ingress to the namespace so no other pod can use the app's read access (enforced where the CNI implements NetworkPolicy). `deploy/install_test.go` pins RBAC, Service, NetworkPolicy and pod shape, each check also run on mutated copies. The opt-in `deploy/install-lb.yaml` (LoadBalancer Service, required `loadBalancerSourceRanges`, `--logs=false`) and the `kube-system` variant are Phase 7 work; neither exists yet, nor does the `--logs` flag.

## Project-Specific Standards

- **Read-only, always:** the app gets get/list/watch RBAC only; it never writes to a cluster and never enables cluster features. Secrets are excluded except named exceptions the PRD lists.
- **Install stays one command:** any change that adds a step to installation needs the operator's approval.
- **Never touch the operator's clusters** (`exact-loon-6dim` included) unless the operator asks in their own turn. Local integration tests use kind on Colima.
- **UI:** white page, text ≥ 12px, no external network dependencies at runtime (the image serves every asset).

## Testing

Unit: `go test -race ./...` (fake clientset, hand-written fakes, httptest TLS). Integration: kind on Colima via `test/kind/up.sh` + `test/kind/probe.sh`, and `test/kind/install-check.sh` against an installed manifest (rollout, `/healthz`, page, stream snapshot, `auth can-i` denials, no ERROR logs, ingress blocked from another namespace; `VK_MUST_FAIL=1` deletes the NetworkPolicy, expects open, restores it). The same script verifies the release on a GKE test cluster (`DEPLOY-gcp`). Playwright (`test/e2e/`, config `web/playwright.config.ts`) runs against the binary, never against `vite dev`. It starts one `--replay` server per recorded stream (`test/e2e/servers.ts`): `deploy-scale` on 127.0.0.1:18080, `node-join` 18081, `missing-sources` 18082, `large-cluster` 18083, a telemetry stream on 18084 that `test/e2e/fixtures/telemetry.mjs` writes into `.agents/scratch/e2e/` first (D35), `deploy-scale` again on 18085 for its recorded Events (opened only by `panel.spec.ts`), `deploy-scale` again on 18086 for replay testing (opened only by `replay.spec.ts`), and `g3-scale.ndjson` on 18087 (150 bubbles, 1,000 pods on 4 nodes, 5 apps at zero fold into overBudget; opened by `perf.spec.ts`). The specs are `web`, `filter`, `wire`, `panel`, `encodings`, `folding`, `budget`, `replay`, `replay-live` (page.route streams: no chain, a chain that grows after playback caught up) and `perf` (32 tests). They include the R10 text-size and R9 same-origin checks (`test/e2e/checks.ts`), each validated in its must-fail state, and the G3 perf probe (≥ 30 fps, with an in-suite must-fail test under a 20× CDP CPU throttle falling under 30 fps). The procedure is `GUIDE-testing` (`fyr record get GUIDE-testing`).

## Deploy

- Local: the binary against your kubeconfig, or kind on Colima. See `DEPLOY-local`.
- GCP: image in Artifact Registry on `exact-loon-6dim`, demo cluster on GKE. See `DEPLOY-gcp`; nothing deploys without the operator's go.
- Remote: not applicable (`DEPLOY-remote`).

## Key Files

| Path | Purpose |
|------|---------|
| `design/mockup-v3.html` | Approved visual reference (simulated data, self-contained) |
| `cmd/visual-kubernetes/` | The binary: serve (live or `--replay`) and `probe` |
| `cmd/visual-kubernetes/embed.go` | Embeds `ui/dist` (the Vite client build, gitignored) and serves it with CSP and same-origin headers |
| `deploy/` | `install.yaml` (namespace, read-only RBAC, Deployment, ClusterIP Service, deny-all-ingress NetworkPolicy) and `install_test.go` + `checks_test.go`, which pin it both ways |
| `examples/` | Manifests and scripts (01-deployment, 02-nodes, 03-hpa, 03b-vpa) for live teaching demos |
| `Dockerfile` | Multi-arch image: node stage builds the page, Go stage cross-compiles, distroless non-root |
| `web/` | The page: TanStack Start SPA (pnpm, Tailwind, d3-force); `vite.config.ts` names the embed dir |
| `web/src/icons/` | Official Kubernetes icons, vendored unmodified with their Apache-2.0 `LICENSE` and source commit |
| `web/src/web/` | The force web: scene, layout, camera, sizing, folding, counters, pan/zoom |
| `web/src/panel/` | The click panel: config, Events, metrics, logs, node conditions, data sources |
| `web/src/replay/` | Teaching replay: player, caption pointing at hub or app, keyboard controls (R, Space, →, Esc) |
| `test/e2e/` | Playwright specs (`web`, `filter`, `wire`, `panel`, `encodings`, `folding`, `budget`, `replay`, `replay-live`, `perf`), the R9/R10 checks (`checks.ts`), the replay servers (`servers.ts`) and the telemetry generator (`fixtures/telemetry.mjs`) |
| `internal/kubelet/` | Kubelet Summary client, TLS always verified (R8, ADR-005) |
| `internal/probe/` | One-shot capability probe and the five source states |
| `internal/watch/` | Discovery + dynamic informers for the RBAC groups (Secrets never listed, forbidden kinds skipped, cache trimmed) |
| `internal/model/` | Live model: entity ids, apps and satellites, edges, S5 folding, deltas and SSE payloads; `modeltest/` builders |
| `internal/replay/` | Causal-chain builder (D17, runs in fixturegen; live wiring deferred D48) |
| `internal/sources/` | 5 s collector: metrics.k8s.io, kubelet network rates, `/readyz`, `/metrics`, Leases → `metrics`, `capabilities`, `cp/*` |
| `internal/stream/` | SSE hub (15 min resume ring), HTTP endpoints, Host guard, replay of recorded streams |
| `test/fixturegen/` | Writes the recorded streams in `test/fixtures/streams/` (deterministic, from `modeltest` objects) |
| `test/kind/` | kind fixture (`up.sh`), probe RBAC and Pod, `probe.sh`, live install check `install-check.sh` |
| `PRD.md` | What and why: problem, goals, the non-negotiable requirements |
| `SDD.md` | How the parts fit: components, flows, decisions |
| `TDD.md` | How each part is built and tested |
| `TODO.md` | The active phase's tasks, the next phases, the backlog |
| `.agents/` | Project customizations and `scratch/` (trail records live in `fyr`'s brain store; `fyr record export` writes `.agents/trail.jsonl` on demand) |
