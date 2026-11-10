# TDD — visual-kubernetes

How each part of visual-kubernetes is built and tested.
Status: draft · Last updated: 2026-11-09 · Requirements: [PRD.md](PRD.md) · Design: [SDD.md](SDD.md)

<!-- The technical design: one section T1… per component M… of SDD.md — its interfaces, data, errors, configuration
     and dependencies — each a summary that points at the SPEC record holding the detail (`fyr record get SPEC-…`),
     never a copy of it. TODO.md tasks name the section they build (→ T1). Kept current by the session that
     changes the code, in the same commit. -->

## T1 — visual-kubernetes binary

Implements: M1 · Spec: SPEC-visual-kubernetes-stream (none yet: the plan writes it before code)

- **Interfaces:** `visual-kubernetes probe [--kubeconfig PATH]` prints `<source> ok|missing|denied|unreachable|error <detail>` per source (`internal/probe`); exit 0 always, 1 when no client can be built, 2 on usage. Planned: an HTTP server serving the embedded UI and an SSE stream.
- **Errors:** `probe.Classify` maps API and TLS errors to the five states, checking TLS errors first. Planned: a missing optional source degrades the view and never crashes the app.
- **Configuration:** in-cluster config, or `--kubeconfig` when run locally. The kubelet Summary client (`internal/kubelet`) is built only from in-cluster config, verifies TLS against the CA bundle and never follows redirects (ADR-005, ADR-006).
- **Watch (`internal/watch`):** discovery keeps the list+watch resources of the 13 RBAC groups (D16), minus Secrets and subresources; a `Limit: 1` list skips forbidden, gone or list-less kinds (one `kind skipped` warning each) while any other error keeps the informer retrying. Each informer trims objects before caching them (`trimForCache`: managedFields, the last-applied annotation, core ConfigMap `data`/`binaryData`) and feeds one goroutine that hands batches of typed objects (CRD kinds stay unstructured) to the model; `Synced()` is the planned `/healthz` condition.
- **Model (`internal/model`):** `Apply` recomputes the drawable view and returns ordered deltas (entity upserts, edge upserts, edge deletes, entity deletes). Ids per D13 and D23 (`app/<ns>/<Kind>/<name>`, `cp/apiserver` hub, `counter/<group>.<Kind>`); apps by top controller owner, Services merged only over exactly one app (D15); HPA, VPA, ConfigMap, PVC, NetworkPolicy, Ingress, Gateway API become satellites (`appId`) or edges (`member`, `hosts`, `selects`, `references`, `routes`, `parent`); unreferenced and undrawn kinds are counted; past S5 (20 nodes, 500 pods, 150 apps) system apps then the smallest fold into `counter/folded.{apps,pods}`. Secrets and Events are never stored (R2).

## T2 — stream and replay

Implements: M2 · Planned in PLAN-2026-11-09-1906 Phases 2 and 5 (SSE endpoint, `/api/objects`, `/api/logs`, causal chains). Built so far: the event payloads (`model.SnapshotEvent`, `model.DeltaEvents`, `model.Record`; `edgeUpsert {edge}` and `edgeDelete {id}` added by plan D22) and the recorded streams: `go run ./test/fixturegen` rewrites `test/fixtures/streams/{deploy-scale,large-cluster,missing-sources,node-join}.ndjson` (snapshot at `atMs` 0, then deltas) byte for byte, and `TestFixturesAreCurrent` fails on stale files.

## T3 — web page

Implements: M3 · Planned in Phases 2–4 (`web/`, SVG + d3-force, filter pill, panel, replay keys). Not built yet.

## T4 — Hubble flows

Implements: M4 · Planned in Phase 5 (auto-detected Relay, `GetFlows`). Not built yet.

## T5 — packaging and examples

Implements: M5 · Planned in Phases 4 and 6 (`deploy/install.yaml`, kube-system variant, Dockerfile, `examples/`). Not built yet.

## Data model

No stored data: the live model and a short timeline are held in memory only.

## Testing strategy

Unit tests (`go test -race ./...`) cover `internal/kubelet`, `internal/probe`, `internal/watch` (fake discovery and dynamic client), `internal/model` (`modeltest` object builders), `test/fixturegen` and the command; integration runs the probe in-cluster on kind (`test/kind/up.sh`, `test/kind/probe.sh`). The tiers are listed in AGENTS.md § Testing. See `GUIDE-testing` (`fyr record get GUIDE-testing`).

## Build, run and deploy

`go build ./...`; container image from the root `Dockerfile` (distroless static, non-root). See the `DEPLOY-local` record.

## Trace

| Component | Section | Verified by |
|---|---|---|
| M1 | T1 | probe and kubelet client: `go test -race ./internal/...`, kind in-cluster probe (Phase 0); watch and model: `go test -race ./internal/watch/... ./internal/model/...` (Phase 1) |
| M2 | T2 | fixtures: `go test ./test/fixturegen/...` (Phase 1); stream not yet |
| M3 | T3 | not yet |
| M4 | T4 | not yet |
| M5 | T5 | not yet |
