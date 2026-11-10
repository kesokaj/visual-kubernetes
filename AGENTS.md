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
pnpm -C web exec playwright test         # e2e: runs the binary with --replay on 127.0.0.1:18080
test/kind/up.sh        # kind cluster on Colima; kubeconfig in .agents/scratch/kind/kubeconfig
go run ./cmd/visual-kubernetes probe --kubeconfig .agents/scratch/kind/kubeconfig
go run ./cmd/visual-kubernetes --kubeconfig .agents/scratch/kind/kubeconfig   # serve on 127.0.0.1:8080
go run ./cmd/visual-kubernetes --replay test/fixtures/streams/deploy-scale.ndjson   # no cluster
test/kind/probe.sh     # image build + kind load + in-cluster probe
go run ./test/fixturegen   # rewrite test/fixtures/streams/*.ndjson after a model change (TestFixturesAreCurrent fails when stale)
```

`visual-kubernetes probe` prints `<source> ok|missing|denied|unreachable|error <detail>` for discovery, leases, events, endpointslices, metrics.k8s.io, kubelet, apiserver-metrics, readyz and hubble-relay, and exits 0 whatever the sources report (1 only when no client can be built, 2 on usage). Always pass `--kubeconfig` locally: without it the standard loading rules pick the operator's current context.

## Architecture

The agreed shape is in `SDD.md` and the open plan record PLAN-2026-11-09-1906-plan-visual-kubernetes-v1-live-spider-web-visual. The approved visual reference is `design/mockup-v3.html`: a force-directed "spider web" on white, apps as bubbles with pods hanging around them, official Kubernetes icons, a small filter pill (all on by default), and click for config, logs and metrics. ADRs: `fyr record list --kind adr`.

The page (`web/src/`) keeps the stream in a `Graph` store (`lib/graph.ts`, events parsed by `lib/wire.ts`). `web/scene.ts` turns it into anchors and links for the groups the filter shows. `web/layout.ts` runs d3-force over those, with the API hub pinned at the centre. `web/camera.ts` auto-fits the web at 16:9 until the presenter zooms or pans; F fits again and V hides the pill. Labels are drawn in screen space so they stay 14/12px at any zoom. The click panel (config, logs, metrics) is Phase 4.

## Project-Specific Standards

- **Read-only, always:** the app gets get/list/watch RBAC only; it never writes to a cluster and never enables cluster features. Secrets are excluded except named exceptions the PRD lists.
- **Install stays one command:** any change that adds a step to installation needs the operator's approval.
- **Never touch the operator's clusters** (`exact-loon-6dim` included) unless the operator asks in their own turn. Local integration tests use kind on Colima.
- **UI:** white page, text ≥ 12px, no external network dependencies at runtime (the image serves every asset).

## Testing

Unit: `go test -race ./...` (fake clientset, hand-written fakes, httptest TLS). Integration: kind on Colima via `test/kind/up.sh` + `test/kind/probe.sh`. Playwright (`test/e2e/`, config `web/playwright.config.ts`) runs against the binary replaying `test/fixtures/streams/deploy-scale.ndjson`, never against `vite dev`. It includes the R10 text-size and R9 same-origin checks, each validated in its must-fail state. The procedure is `GUIDE-testing` (`fyr record get GUIDE-testing`).

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
| `web/` | The page: TanStack Start SPA (pnpm, Tailwind, d3-force); `vite.config.ts` names the embed dir |
| `web/src/icons/` | Official Kubernetes icons, vendored unmodified with their Apache-2.0 `LICENSE` and source commit |
| `test/e2e/` | Playwright specs (`web`, `filter`, `wire`) and the shared R9/R10 checks |
| `internal/kubelet/` | Kubelet Summary client, TLS always verified (R8, ADR-005) |
| `internal/probe/` | One-shot capability probe and the five source states |
| `internal/watch/` | Discovery + dynamic informers for the RBAC groups (Secrets never listed, forbidden kinds skipped, cache trimmed) |
| `internal/model/` | Live model: entity ids, apps and satellites, edges, S5 folding, deltas and SSE payloads; `modeltest/` builders |
| `internal/sources/` | 5 s collector: metrics.k8s.io, kubelet network rates, `/readyz`, `/metrics`, Leases → `metrics`, `capabilities`, `cp/*` |
| `internal/stream/` | SSE hub (15 min resume ring), HTTP endpoints, Host guard, replay of recorded streams |
| `test/fixturegen/` | Writes the recorded streams in `test/fixtures/streams/` (deterministic, from `modeltest` objects) |
| `test/kind/` | kind fixture (`up.sh`), probe RBAC and Pod, `probe.sh` |
| `PRD.md` | What and why: problem, goals, the non-negotiable requirements |
| `SDD.md` | How the parts fit: components, flows, decisions |
| `TDD.md` | How each part is built and tested |
| `TODO.md` | The active phase's tasks, the next phases, the backlog |
| `.agents/` | Project customizations and `scratch/` (trail records live in `fyr`'s brain store; `fyr record export` writes `.agents/trail.jsonl` on demand) |
