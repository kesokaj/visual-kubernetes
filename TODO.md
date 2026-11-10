# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Phase 5 — Teaching replay and scenarios · PLAN-2026-11-09-1906-plan-visual-kubernetes-v1-live-spider-web-visual · goal: causal chains, replay keys, example scenarios. Phase 4 done 2026-11-10 (the R8–R10 box is partial: `fyr check` still exits 1 on 17 findings outside Phase 4's files).

- [ ] Causal chain builder (D17) emitting `chain` events
- [ ] Replay keys: Space pause, → step, R replay the last chain
- [ ] `examples/01-deployment`, `02-nodes`, `03-hpa`, `03b-vpa`, each with a short script
- [ ] Checkpoint

## Next

- Phase 6 — Hubble traffic.
- Phase 7 — Packaging, install and live e2e.

## Backlog

- Size-metric switch (PRD C3; added 2026-11-09, plan D11).
- CRD kinds outside the named RBAC groups (added 2026-11-09, plan D16).
- OTLP receiver as a traffic source (added 2026-11-09, grill N4/row 6).
- Cloud Audit Logs stream for real API-check steps (added 2026-11-09, grill Q7).
- Elliptical, label-aware spacing to stop label overlaps (added 2026-11-09, mockup v3 nit).
- Watch Events from one API (core/v1 and events.k8s.io/v1 are both cached) and trim them to the streamed fields; deferred from Phase 2 because it edits `internal/watch`, outside that phase's files; runs as its own Task after Phase 4, plan D29 (added 2026-11-10, security review 2026-11-10 finding 1).
- `test/kind/serve.sh`: scripted in-cluster serve check (port-forward + curl `/healthz`, `/api/stream`) replacing the manual Phase 2 run; fold into Phase 7's install e2e (added 2026-11-10, Phase 2 review finding 2).
- Server-side `event` SSE emission so the panel shows live Events (page side ready, plan D32); do it with the D29 Task (added 2026-11-10, Phase 4 review finding 1).
- Perf probe at G3 scale, 150 bubbles / 1,000 pods (the current fixture gives about 107 / 498) (added 2026-11-10, Phase 4 review finding 4).
- Docs refresh (approved by the operator 2026-11-10 13:23, queued as a Task before Phase 5): AGENTS.md / TDD.md `web/*.ts` paths, "click panel is Phase 4", SDD.md `internal/hubble`, GUIDE-testing (5 servers, 27 tests), the remaining `fyr check` findings (added 2026-11-10, Phase 4 review and verifier).
