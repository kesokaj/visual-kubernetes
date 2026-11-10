# visual-kubernetes

A read-only, real-time visualizer of Kubernetes internals on one white page, for live teaching demos.

## Quick start

Build, run and test commands are in `AGENTS.md` § Setup commands (`go run ./cmd/visual-kubernetes --replay test/fixtures/streams/deploy-scale.ndjson` shows the page without a cluster, after `pnpm -C web build`). The planned install is one command:

```bash
kubectl apply -f install.yaml        # planned: one Deployment + read-only ClusterRole
kubectl -n <namespace> port-forward deploy/visual-kubernetes 8080   # planned
```

The commands become the contract in `AGENTS.md` § Setup commands; the `GUIDE-testing` record (`fyr record get GUIDE-testing`) has the tiers behind them.

## Architecture

One Go binary watches the cluster with client-go informers (read-only) and keeps the live model in memory. It streams changes over SSE to an embedded TanStack Start page that draws a force-directed web (SVG + d3-force; Canvas for traffic is planned). The visual reference is `design/mockup-v3.html`.

```
cmd/visual-kubernetes/  # the binary: serve (live or --replay), probe, the embedded page
internal/               # the packages, one responsibility each
web/                    # the TanStack Start frontend, built into the binary
test/                   # fixtures, Playwright e2e, kind integration
design/                 # approved mockup
.agents/                # project customizations and scratch/ (trail records live in fyr's brain store, read with fyr record)
```

## Configuration

No settings yet; the code reads none.

## Deploy

How it runs locally and on GCP is in the `DEPLOY-local` and `DEPLOY-gcp` records; a release goes through `/fyr-preflight`. The UI has no auth: never expose it beyond `kubectl port-forward`.

## Where things are

Decisions: `fyr record list --kind adr`. What changed: `--kind change`. What went wrong and the rule that came out of it: `--kind bug`. The next session starts from the newest handoff, `fyr record list --kind handoff`.
