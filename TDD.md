# TDD — visual-kubernetes

How each part of visual-kubernetes is built and tested.
Status: draft · Last updated: 2026-11-10 · Requirements: [PRD.md](PRD.md) · Design: [SDD.md](SDD.md)

<!-- The technical design: one section T1… per component M… of SDD.md — its interfaces, data, errors, configuration
     and dependencies — each a summary that points at the SPEC record holding the detail (`fyr record get SPEC-…`),
     never a copy of it. TODO.md tasks name the section they build (→ T1). Kept current by the session that
     changes the code, in the same commit. -->

## T1 — visual-kubernetes binary

Implements: M1 · Spec: SPEC-visual-kubernetes-stream (none yet: the plan writes it before code)

- **Interfaces:** `visual-kubernetes [--kubeconfig PATH] [--addr HOST:PORT] [--replay FILE]` serves the stream (`--addr` defaults to `127.0.0.1:8080` with `--kubeconfig` or `--replay`, `:8080` in-cluster, plan D26; `--replay` with `--kubeconfig` is a usage error). `visual-kubernetes probe [--kubeconfig PATH]` prints `<source> ok|missing|denied|unreachable|error <detail>` per source (`internal/probe`); exit 0 always, 1 when no client can be built, 2 on usage. The serve command embeds the page build (`cmd/visual-kubernetes/embed.go`, Phase 3). Planned in Phase 7: `--logs=false`, under which `/api/logs` answers 403 (plan D38, ADR-008).
- **Errors:** `probe.Classify` maps API and TLS errors to the five states, checking TLS errors first. A missing optional source degrades the view (a `capabilities` event names it) and never crashes the app.
- **Configuration:** in-cluster config, or `--kubeconfig` when run locally. The kubelet Summary client (`internal/kubelet`) is built only from in-cluster config, verifies TLS against the CA bundle and never follows redirects (ADR-005, ADR-006).
- **Watch (`internal/watch`):** discovery keeps the list+watch resources of the 13 RBAC groups (D16), minus Secrets and subresources; a `Limit: 1` list skips forbidden, gone or list-less kinds (one `kind skipped` warning each) while any other error keeps the informer retrying. Events are watched through one API, events.k8s.io/v1 when its list check passes, else core/v1 (D29). Each informer trims objects before caching them (`trimForCache`: managedFields, the last-applied annotation, core ConfigMap `data`/`binaryData`; Events keep only identity, regarding, type, reason, note/message (cut at 1024 bytes) and their times; an Event about a Secret is never streamed) and feeds one goroutine that hands batches of typed objects (CRD kinds stay unstructured) to the model; `Synced()` is the `/healthz` condition (readiness in `deploy/install.yaml`; liveness is a TCP check, so a slow sync never restarts the pod).
- **Model (`internal/model`):** `Apply` recomputes the drawable view and returns ordered deltas (entity upserts, edge upserts, edge deletes, entity deletes). Ids per D13 and D23 (`app/<ns>/<Kind>/<name>`, `cp/apiserver` hub, `counter/<group>.<Kind>`); apps by top controller owner, Services merged only over exactly one app (D15); HPA, VPA, ConfigMap, PVC, NetworkPolicy, Ingress, Gateway API become satellites (`appId`) or edges (`member`, `hosts`, `selects`, `references`, `routes`, `parent`); unreferenced and undrawn kinds are counted; past S5 (20 nodes, 500 pods, 150 apps) system apps then the smallest fold into `counter/folded.{apps,pods}`. Secrets and Events are never stored (R2): `ApplyEvents` returns `Apply`'s deltas plus one `event` per added or updated Event (`{k8sEvent:{id,type,reason,message,at}}`, `id` = the regarding object's entity id, `at` its newest time; D32), which the serve command publishes.
- **Sources (`internal/sources`):** a `Collector` runs every 5 s (plan D24). It reads metrics.k8s.io pod usage (Quantities summed per pod, rounded once), kubelet Summary network bytes turned into rates over a 10 s window (in-cluster only; hostNetwork pods and pods missing from the model are skipped, 8 nodes at a time, 4 s each), `/readyz?verbose=true` every cycle and the apiserver `/metrics` object counts every 6th. It infers `cp/apiserver`, `cp/etcd`, `cp/scheduler`, `cp/controller-manager` from readyz checks and Lease freshness, tagging each with the label `visual-kubernetes/evidence` = `lease|readyz|none` (S4). Cycles wait for the informers' first sync (`Deps.Ready`, latched open once true). It emits a `metrics` event whose `sizeBy` is `network`, else `cpu`, else `pods`, plus a `capabilities` event and `cp/*` upserts only when they change.

## T2 — stream and replay

Implements: M2 · Built in Phase 2 (`internal/stream`); causal chains planned in Phase 5. Event payloads come from `internal/model` (`model.SnapshotEvent`, `model.DeltaEvents`, `model.Record`; `edgeUpsert {edge}` and `edgeDelete {id}` added by plan D22; `event {k8sEvent}` from `model.ApplyEvents`, D32).

- **Hub:** keeps the current view plus a 15 min ring (capped) of numbered events. Ids start at the process start time in µs, so ids from an earlier process never resume by accident. `Subscribe` replays only the missed events when `Last-Event-ID` falls in `[oldest-1, last]`; any other id (absent, malformed, too old, foreign) gets a fresh `snapshot`. A subscriber whose buffer fills is dropped and reconnects with its last id.
- **HTTP:** `GET /api/stream` (SSE: `snapshot`, `upsert`, `delete`, `edgeUpsert`, `edgeDelete`, `metrics`, `capabilities`, `event`), `GET /healthz` (503 until the informers sync), `GET /api/objects?id=` (the object as YAML, model first, managedFields stripped, Secrets refused; 400 malformed id, 404 unknown) and `GET /api/logs?id=&container=&tailLines=` (default 200, capped at 1000 lines and 1 MiB). Upstream errors map to 404/403/502 without the API detail; every response sets `nosniff`. `HostGuard` answers 403 unless the Host is an IP literal, `localhost` or the `--addr` name, so a DNS-rebinding page cannot read the unauthenticated endpoints.
- **Replay:** `--replay FILE` validates a recorded `.ndjson` stream (snapshot first, state and `event` records decoded, an `event` without an id refused, other plain-word events passed through), waits for the first subscriber, then plays it once on its `atMs` timing (plan D25); `/api/objects` returns the entity as YAML and `/api/logs` a note that replay has no logs.
- **Recorded streams:** `go run ./test/fixturegen` rewrites `test/fixtures/streams/{deploy-scale,large-cluster,missing-sources,node-join}.ndjson` (snapshot at `atMs` 0, then deltas and the `event` records of `deploy-scale` and `node-join`) byte for byte, and `TestFixturesAreCurrent` fails on stale files.

## T3 — web page

Implements: M3 · Built in Phases 3 and 4: `web/` (TanStack Start SPA, pnpm, Tailwind).

- **Stream and state (`web/src/lib/`):** `wire.ts` parses SSE guardedly, dropping invalid items one by one; `graph.ts` is the external store; `telemetry.ts` keeps the latest metrics sample per id, 90 s of sparkline history, `sizeBy` (pods until the first `metrics` event, D33) and a bounded Event log; `motion.ts` derives pulses and pod travel from deltas.
- **Force web (`web/src/web/`):** `scene.ts` turns the filtered graph into anchors and links; `folding.ts` folds static control-plane pods behind their `cp/*` entity when it exists (D30, D34); `sizing.ts` sizes apps by area from network, CPU or pod count and ranks the top 3 for halos (S2); `layout.ts` runs d3-force with the hub pinned and reheats only on structural change (D36); `camera.ts` auto-fits at 16:9 and hands over on zoom or pan; `panzoom.ts` maps clicks and drags; `Counters.tsx` shows `counter/*` entities and the 150-bubble budget's overflow (`counter/page.overBudget`, D33); labels are drawn in screen space.
- **Filter and panel:** `filter/FilterPill.tsx` (8 groups, all on). `panel/` is the click panel: config YAML from `/api/objects` (an app requests its top workload), Events, metrics sparklines, pod logs from `/api/logs` (warning that logs can carry secrets), node conditions read from the Node YAML, `cp/*` evidence and the API server's data sources. Esc, the close button or a click on empty space closes it. Live Events come from the server's `event` SSE (D29, D32).
- **Icons:** vendored in `web/src/icons/` (Apache-2.0) and turned into one sprite.
- **Planned:** replay keys (Phase 5); traffic particles (Phase 6); the "logs are off on this install" message under `--logs=false` (Phase 7, D38).

## T4 — Hubble flows

Implements: M4 · Planned in Phase 6 (auto-detected Relay, `GetFlows`). Not built yet.

## T5 — packaging and examples

Implements: M5 · Built: `deploy/install.yaml` (namespace with PSA `restricted`, ServiceAccount, the get/list/watch ClusterRole and its binding, the Deployment with readiness on `/healthz` and a TCP liveness check, a ClusterIP Service, a NetworkPolicy denying all ingress) and the multi-arch `Dockerfile` (node stage builds the page, Go stage cross-compiles, distroless non-root). `deploy/install_test.go` runs each check in `deploy/checks_test.go` on the file (must pass) and on mutated copies (must fail): RBAC read-only (no write verb, no `*` inside any string, Secrets only by the one name, no aggregationRule, one binding to the app's ServiceAccount), ClusterIP only, deny-all ingress, restricted pod with a released image, and the kind probe's ClusterRole equal to the installed one. Planned: `examples/` (Phase 5); the kube-system variant and the opt-in LoadBalancer variant with required `loadBalancerSourceRanges` and `--logs=false` (Phase 7, ADR-008).

## Data model

No stored data: the live model and a short timeline are held in memory only.

## Testing strategy

Unit tests (`go test -race ./...`) cover `internal/kubelet`, `internal/probe`, `internal/watch` (fake discovery and dynamic client), `internal/model` (`modeltest` object builders), `internal/sources` (hand-written fakes and `testdata/summary.json`, a Summary shaped after the kubelet API with a hostNetwork pod), `internal/stream` (hub with an injected clock, SSE over `httptest`, `synctest` replay timing), `test/fixturegen` and the command (replay serve and shutdown, page routing, headers and hash-pinned CSP, the embed dir pinned to the Vite outDir, the page's wire lists pinned to the model, vendored icons free of active content). Playwright (`test/e2e/`) drives Chromium at 1600×900 against the binary, one `--replay` server per recorded stream (`test/e2e/servers.ts`, ports 18080–18085, D35): `deploy-scale` (twice: 18085 is opened only by `panel.spec.ts`, to see its recorded Events), `node-join`, `missing-sources`, `large-cluster`, and a telemetry stream (metrics every second, Events every five) written by `test/e2e/fixtures/telemetry.mjs` into `.agents/scratch/e2e/`. Its 28 tests cover the web, zoom/pan/F, the filter pill and V, stream guards, the click panel, encodings (pulse, travel, sizing, halos), folding and the 150-bubble budget, the perf probe (≥ 30 fps; checked failing under a 20× CPU throttle once by hand, TEST-2026-11-10-1319, with no throttle test in the suite yet; run at about 107 bubbles / 498 pods until Phase 5's G3-scale fixture, D42), and the R9/R10 checks, each validated in its must-fail state. `wire.spec.ts` and `budget.spec.ts` serve a hand-written `/api/stream` through `page.route` instead of a replay (D45). Integration runs the probe in-cluster on kind (`test/kind/up.sh`, `test/kind/probe.sh`); the server's Phase 2 live check was a manual run (a scratch Deployment, port-forward, curl of `/api/stream`) whose output is in the Phase 2 `testing` record; since Phase 7 `test/kind/install-check.sh` scripts it against `deploy/install.yaml` (rollout, `/healthz`, page, stream snapshot, `auth can-i` denials, ingress blocked from another namespace, both ways with `VK_MUST_FAIL=1`). The tiers are listed in AGENTS.md § Testing. See `GUIDE-testing` (`fyr record get GUIDE-testing`).

## Build, run and deploy

`go build ./...`; container image from the root `Dockerfile` (distroless static, non-root). See the `DEPLOY-local` record.

## Trace

| Component | Section | Verified by |
|---|---|---|
| M1 | T1 | probe and kubelet client: `go test -race ./internal/...`, kind in-cluster probe (Phase 0); watch and model: `go test -race ./internal/watch/... ./internal/model/...` (Phase 1); sources and serve: `go test -race ./internal/sources/... ./cmd/...`, manual kind live check (Phase 2) |
| M2 | T2 | fixtures: `go test ./test/fixturegen/...` (Phase 1); hub, SSE, endpoints, replay: `go test -race ./internal/stream/...`, manual kind live check (Phase 2) |
| M3 | T3 | page server: `go test -race ./cmd/...`; page: `pnpm -C web build && pnpm -C web exec playwright test` (web, filter, wire: Phase 3; panel, encodings, folding, budget, perf: Phase 4) |
| M4 | T4 | not yet |
| M5 | T5 | `go test ./deploy/...` (both ways); kind live install (rollout, port-forward, `auth can-i`, cross-namespace ingress refused); container smoke (`--read-only --replay`) |
