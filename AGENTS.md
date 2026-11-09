# AGENTS.md

## Project Overview

`visual-kubernetes` is a read-only, real-time visualizer of a Kubernetes cluster's internals on one white page, built for live teaching demos. Presenters use it to show what happens when someone adds a Deployment, scales with HPA/VPA, adds worker nodes, or sends traffic between services. It installs as one Deployment named `visual-kubernetes`. It installs by default into its own namespace `visual-kubernetes`, with a `kube-system` variant manifest (PRD S1; GKE Autopilot forbids workloads in `kube-system`). It works on any conformant cluster that grants its read-only RBAC. It uses built-in sources first (watches, Events, Leases, metrics.k8s.io, kubelet stats, API server `/metrics`). Optional traffic sources (Hubble Relay, OTel) are auto-detected, never installed. The app never changes a cluster.

## Setup Commands

One Go module (`github.com/kesokaj/visual-kubernetes`, go 1.27, client-go v0.37). Phase 0 ships only the `probe` subcommand; the UI server (embedded TanStack Start over SSE) lands in Phase 2.

```bash
go build ./...
go vet ./... && go test -race ./...
test/kind/up.sh        # kind cluster on Colima; kubeconfig in .agents/scratch/kind/kubeconfig
go run ./cmd/visual-kubernetes probe --kubeconfig .agents/scratch/kind/kubeconfig
test/kind/probe.sh     # image build + kind load + in-cluster probe
```

`visual-kubernetes probe` prints `<source> ok|missing|denied|unreachable|error <detail>` for discovery, leases, events, endpointslices, metrics.k8s.io, kubelet, apiserver-metrics, readyz and hubble-relay, and exits 0 whatever the sources report (1 only when no client can be built, 2 on usage). Always pass `--kubeconfig` locally: without it the standard loading rules pick the operator's current context.

## Architecture

There is no UI code yet; the agreed shape is in `SDD.md` and the open plan record PLAN-2026-11-09-1906-plan-visual-kubernetes-v1-live-spider-web-visual. The approved visual reference is `design/mockup-v3.html`: a force-directed "spider web" on white, apps as bubbles with pods hanging around them, official Kubernetes icons, a small filter pill (all on by default), and click for config, logs and metrics. ADRs: `fyr record list --kind adr`.

## Project-Specific Standards

- **Read-only, always:** the app gets get/list/watch RBAC only; it never writes to a cluster and never enables cluster features. Secrets are excluded except named exceptions the PRD lists.
- **Install stays one command:** any change that adds a step to installation needs the operator's approval.
- **Never touch the operator's clusters** (`exact-loon-6dim` included) unless the operator asks in their own turn. Local integration tests use kind on Colima.
- **UI:** white page, text ≥ 12px, no external network dependencies at runtime (the image serves every asset).

## Testing

Unit: `go test -race ./...` (fake clientset, hand-written fakes, httptest TLS). Integration: kind on Colima via `test/kind/up.sh` + `test/kind/probe.sh`. Playwright against the binary with a recorded event stream arrives in Phase 3. The procedure is `GUIDE-testing` (`fyr record get GUIDE-testing`).

## Deploy

- Local: the binary against your kubeconfig, or kind on Colima. See `DEPLOY-local`.
- GCP: image in Artifact Registry on `exact-loon-6dim`, demo cluster on GKE. See `DEPLOY-gcp`; nothing deploys without the operator's go.
- Remote: not applicable (`DEPLOY-remote`).

## Key Files

| Path | Purpose |
|------|---------|
| `design/mockup-v3.html` | Approved visual reference (simulated data, self-contained) |
| `cmd/visual-kubernetes/` | The binary's entry point (`probe` subcommand in Phase 0) |
| `internal/kubelet/` | Kubelet Summary client, TLS always verified (R8, ADR-005) |
| `internal/probe/` | One-shot capability probe and the five source states |
| `test/kind/` | kind fixture (`up.sh`), probe RBAC and Pod, `probe.sh` |
| `PRD.md` | What and why: problem, goals, the non-negotiable requirements |
| `SDD.md` | How the parts fit: components, flows, decisions |
| `TDD.md` | How each part is built and tested |
| `TODO.md` | The active phase's tasks, the next phases, the backlog |
| `.agents/` | Project customizations and `scratch/` (trail records live in `fyr`'s brain store; `fyr record export` writes `.agents/trail.jsonl` on demand) |
