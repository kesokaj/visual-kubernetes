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

## Using the page

| Action | Effect |
|--------|--------|
| Click a bubble | Opens the panel for it (click a pod chip in an app's panel to open that pod) |
| Esc, ×, or a click on empty space | Closes the panel |
| Wheel / drag | Zoom and pan; the camera stops auto-fitting |
| F | Fit the whole web again and resume auto-fit |
| V | Hide or show the filter pill |

The panel shows, per object:

- **App and pod:** a summary, metrics sparklines for the last 90 s (network rx+tx, CPU, memory, whichever the cluster reports), Events (once the server streams them: the page reads the `event` stream contract, the server side is a backlog Task, D29), the pod's last 200 log lines from `/api/logs` (Refresh re-reads them), and the YAML from `/api/objects`. An app's config is its top workload (for example `apps/Deployment/shop/web`); replay mode serves only the recorded entities, so that YAML is a 404 there.
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

No settings yet; the code reads none.

## Deploy

How it runs locally and on GCP is in the `DEPLOY-local` and `DEPLOY-gcp` records; a release goes through `/fyr-preflight`. The UI has no auth: never expose it beyond `kubectl port-forward`.

## Where things are

Decisions: `fyr record list --kind adr`. What changed: `--kind change`. What went wrong and the rule that came out of it: `--kind bug`. The next session starts from the newest handoff, `fyr record list --kind handoff`.
