# TDD — visual-kubernetes

How each part of visual-kubernetes is built and tested.
Status: draft · Last updated: 2026-11-09 · Requirements: [PRD.md](PRD.md) · Design: [SDD.md](SDD.md)

<!-- The technical design: one section T1… per component M… of SDD.md — its interfaces, data, errors, configuration
     and dependencies — each a summary that points at the SPEC record holding the detail (`fyr record get SPEC-…`),
     never a copy of it. TODO.md tasks name the section they build (→ T1). Kept current by the session that
     changes the code, in the same commit. -->

## T1 — visual-kubernetes binary

Implements: M1 · Spec: SPEC-visual-kubernetes-stream (none yet: the plan writes it before code)

- **Interfaces:** not yet. Planned: an HTTP server serving the embedded UI and an SSE stream.
- **Errors:** not yet. Planned: a missing optional source degrades the view and never crashes the app.
- **Configuration:** not yet. Planned: in-cluster config, or a kubeconfig when run locally.

## T2 — stream and replay

Implements: M2 · Planned in PLAN-2026-11-09-1906 Phases 1 and 4 (SSE events, `/api/objects`, `/api/logs`, causal chains). Not built yet.

## T3 — web page

Implements: M3 · Planned in Phases 2–4 (`web/`, SVG + d3-force, filter pill, panel, replay keys). Not built yet.

## T4 — Hubble flows

Implements: M4 · Planned in Phase 5 (auto-detected Relay, `GetFlows`). Not built yet.

## T5 — packaging and examples

Implements: M5 · Planned in Phases 4 and 6 (`deploy/install.yaml`, kube-system variant, Dockerfile, `examples/`). Not built yet.

## Data model

No stored data: the live model and a short timeline are held in memory only.

## Testing strategy

No tests yet. The planned tiers are listed in AGENTS.md § Testing. See `GUIDE-testing` (`fyr record get GUIDE-testing`).

## Build, run and deploy

No build yet. See the `DEPLOY-local` record.

## Trace

| Component | Section | Verified by |
|---|---|---|
| M1 | T1 | not yet |
| M2 | T2 | not yet |
| M3 | T3 | not yet |
| M4 | T4 | not yet |
| M5 | T5 | not yet |
