# TDD — visual-kubernetes

How each part of visual-kubernetes is built and tested.
Status: draft · Last updated: 2026-11-10 · Requirements: [PRD.md](PRD.md) · Design: [SDD.md](SDD.md)

<!-- The technical design: one section T1… per component M… of SDD.md — its interfaces, data, errors, configuration
     and dependencies — each a summary that points at the SPEC record holding the detail (`fyr record get SPEC-…`),
     never a copy of it. TODO.md tasks name the section they build (→ T1). Kept current by the session that
     changes the code, in the same commit. -->

## T1 — visual-kubernetes binary

Implements: M1 · Spec: SPEC-visual-kubernetes-stream (none yet: the plan writes it before code)

- **Interfaces:** `visual-kubernetes [--kubeconfig PATH] [--addr HOST:PORT] [--replay FILE]` serves the stream (`--addr` defaults to `127.0.0.1:8080` with `--kubeconfig` or `--replay`, `:8080` in-cluster, plan D26; `--replay` with `--kubeconfig` is a usage error). `visual-kubernetes probe [--kubeconfig PATH]` prints `<source> ok|missing|denied|unreachable|error <detail>` per source (`internal/probe`); exit 0 always, 1 when no client can be built, 2 on usage. Planned: the embedded UI (Phase 3).
- **Errors:** `probe.Classify` maps API and TLS errors to the five states, checking TLS errors first. A missing optional source degrades the view (a `capabilities` event names it) and never crashes the app.
- **Configuration:** in-cluster config, or `--kubeconfig` when run locally. The kubelet Summary client (`internal/kubelet`) is built only from in-cluster config, verifies TLS against the CA bundle and never follows redirects (ADR-005, ADR-006).
- **Watch (`internal/watch`):** discovery keeps the list+watch resources of the 13 RBAC groups (D16), minus Secrets and subresources; a `Limit: 1` list skips forbidden, gone or list-less kinds (one `kind skipped` warning each) while any other error keeps the informer retrying. Each informer trims objects before caching them (`trimForCache`: managedFields, the last-applied annotation, core ConfigMap `data`/`binaryData`) and feeds one goroutine that hands batches of typed objects (CRD kinds stay unstructured) to the model; `Synced()` is the planned `/healthz` condition.
- **Model (`internal/model`):** `Apply` recomputes the drawable view and returns ordered deltas (entity upserts, edge upserts, edge deletes, entity deletes). Ids per D13 and D23 (`app/<ns>/<Kind>/<name>`, `cp/apiserver` hub, `counter/<group>.<Kind>`); apps by top controller owner, Services merged only over exactly one app (D15); HPA, VPA, ConfigMap, PVC, NetworkPolicy, Ingress, Gateway API become satellites (`appId`) or edges (`member`, `hosts`, `selects`, `references`, `routes`, `parent`); unreferenced and undrawn kinds are counted; past S5 (20 nodes, 500 pods, 150 apps) system apps then the smallest fold into `counter/folded.{apps,pods}`. Secrets and Events are never stored (R2).
- **Sources (`internal/sources`):** a `Collector` runs every 5 s (plan D24). It reads metrics.k8s.io pod usage (Quantities summed per pod, rounded once), kubelet Summary network bytes turned into rates over a 10 s window (in-cluster only; hostNetwork pods and pods missing from the model are skipped, 8 nodes at a time, 4 s each), `/readyz?verbose=true` every cycle and the apiserver `/metrics` object counts every 6th. It infers `cp/apiserver`, `cp/etcd`, `cp/scheduler`, `cp/controller-manager` from readyz checks and Lease freshness, tagging each with the label `visual-kubernetes/evidence` = `lease|readyz|none` (S4). Cycles wait for the informers' first sync (`Deps.Ready`, latched open once true). It emits a `metrics` event whose `sizeBy` is `network`, else `cpu`, else `pods`, plus a `capabilities` event and `cp/*` upserts only when they change.

## T2 — stream and replay

Implements: M2 · Built in Phase 2 (`internal/stream`); causal chains planned in Phase 5. Event payloads come from `internal/model` (`model.SnapshotEvent`, `model.DeltaEvents`, `model.Record`; `edgeUpsert {edge}` and `edgeDelete {id}` added by plan D22).

- **Hub:** keeps the current view plus a 15 min ring (capped) of numbered events. Ids start at the process start time in µs, so ids from an earlier process never resume by accident. `Subscribe` replays only the missed events when `Last-Event-ID` falls in `[oldest-1, last]`; any other id (absent, malformed, too old, foreign) gets a fresh `snapshot`. A subscriber whose buffer fills is dropped and reconnects with its last id.
- **HTTP:** `GET /api/stream` (SSE: `snapshot`, `upsert`, `delete`, `edgeUpsert`, `edgeDelete`, `metrics`, `capabilities`), `GET /healthz` (503 until the informers sync), `GET /api/objects?id=` (the object as YAML, model first, managedFields stripped, Secrets refused; 400 malformed id, 404 unknown) and `GET /api/logs?id=&container=&tailLines=` (default 200, capped at 1000 lines and 1 MiB). Upstream errors map to 404/403/502 without the API detail; every response sets `nosniff`. `HostGuard` answers 403 unless the Host is an IP literal, `localhost` or the `--addr` name, so a DNS-rebinding page cannot read the unauthenticated endpoints.
- **Replay:** `--replay FILE` validates a recorded `.ndjson` stream (snapshot first, state events decoded, other plain-word events passed through), waits for the first subscriber, then plays it once on its `atMs` timing (plan D25); `/api/objects` returns the entity as YAML and `/api/logs` a note that replay has no logs.
- **Recorded streams:** `go run ./test/fixturegen` rewrites `test/fixtures/streams/{deploy-scale,large-cluster,missing-sources,node-join}.ndjson` (snapshot at `atMs` 0, then deltas) byte for byte, and `TestFixturesAreCurrent` fails on stale files.

## T3 — web page

Implements: M3 · Built in Phase 3: `web/` (TanStack Start SPA, pnpm, Tailwind) with `lib/wire.ts` (guarded SSE parsing, invalid items dropped one by one), `lib/graph.ts` (external store), `web/scene.ts` (filter → anchors and links), `web/layout.ts` (d3-force, hub pinned), `web/camera.ts` (16:9 auto-fit, zoom, pan) and screen-space labels; `filter/FilterPill.tsx` (8 groups). Icons are vendored in `web/src/icons/` and turned into one sprite. The click panel and replay keys are planned in Phase 4.

## T4 — Hubble flows

Implements: M4 · Planned in Phase 5 (auto-detected Relay, `GetFlows`). Not built yet.

## T5 — packaging and examples

Implements: M5 · Planned in Phases 4 and 6 (`deploy/install.yaml`, kube-system variant, Dockerfile, `examples/`). Not built yet.

## Data model

No stored data: the live model and a short timeline are held in memory only.

## Testing strategy

Unit tests (`go test -race ./...`) cover `internal/kubelet`, `internal/probe`, `internal/watch` (fake discovery and dynamic client), `internal/model` (`modeltest` object builders), `internal/sources` (hand-written fakes and `testdata/summary.json`, a Summary shaped after the kubelet API with a hostNetwork pod), `internal/stream` (hub with an injected clock, SSE over `httptest`, `synctest` replay timing), `test/fixturegen` and the command (replay serve and shutdown, page routing, headers and hash-pinned CSP, the embed dir pinned to the Vite outDir, the page's wire lists pinned to the model, vendored icons free of active content). Playwright (`test/e2e/`) drives Chromium against the binary replaying `test/fixtures/streams/deploy-scale.ndjson`: the web, zoom/pan/F, the filter pill and V, stream guards, and the R9/R10 checks, each validated in its must-fail state. Integration runs the probe in-cluster on kind (`test/kind/up.sh`, `test/kind/probe.sh`); the server's Phase 2 live check was a manual run (a scratch Deployment, port-forward, curl of `/api/stream`) whose output is in the Phase 2 `testing` record, until Phase 7's install manifest makes it scriptable. The tiers are listed in AGENTS.md § Testing. See `GUIDE-testing` (`fyr record get GUIDE-testing`).

## Build, run and deploy

`go build ./...`; container image from the root `Dockerfile` (distroless static, non-root). See the `DEPLOY-local` record.

## Trace

| Component | Section | Verified by |
|---|---|---|
| M1 | T1 | probe and kubelet client: `go test -race ./internal/...`, kind in-cluster probe (Phase 0); watch and model: `go test -race ./internal/watch/... ./internal/model/...` (Phase 1); sources and serve: `go test -race ./internal/sources/... ./cmd/...`, manual kind live check (Phase 2) |
| M2 | T2 | fixtures: `go test ./test/fixturegen/...` (Phase 1); hub, SSE, endpoints, replay: `go test -race ./internal/stream/...`, manual kind live check (Phase 2) |
| M3 | T3 | page server: `go test -race ./cmd/...`; page: `pnpm -C web build && pnpm -C web exec playwright test` (Phase 3) |
| M4 | T4 | not yet |
| M5 | T5 | not yet |
