# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Phase 4 — Panel, encodings, folding and performance · PLAN-2026-11-09-1906-plan-visual-kubernetes-v1-live-spider-web-visual · goal: click panel, throughput sizing and pulses, corner counters, perf probe. Phase 3 done 2026-11-10.

- [ ] Click panel per object type (config, Events, metrics, logs, node conditions, capabilities); Esc closes
- [ ] Size by throughput with fallbacks, top-3 halo, pulses; pods move along threads to a joining node
- [ ] Corner counters for `counter/*` entities and folded apps; static control-plane pods fold behind `cp/*` (D30)
- [ ] Perf probe (G3/S5): median frame time over 10 s on `large-cluster.ndjson`
- [ ] PRD.md R8–R10 `enforced by:` comments name the existing tests, comment lines only, with an ADR (D31)
- [ ] Checkpoint

## Next

- Phase 5 — Teaching replay and scenarios.
- Phase 6 — Hubble traffic.

## Backlog

- Size-metric switch (PRD C3; added 2026-11-09, plan D11).
- CRD kinds outside the named RBAC groups (added 2026-11-09, plan D16).
- OTLP receiver as a traffic source (added 2026-11-09, grill N4/row 6).
- Cloud Audit Logs stream for real API-check steps (added 2026-11-09, grill Q7).
- Elliptical, label-aware spacing to stop label overlaps (added 2026-11-09, mockup v3 nit).
- Watch Events from one API (core/v1 and events.k8s.io/v1 are both cached) and trim them to the streamed fields; deferred from Phase 2 because it edits `internal/watch`, outside that phase's files; runs as its own Task after Phase 4, plan D29 (added 2026-11-10, security review 2026-11-10 finding 1).
- `test/kind/serve.sh`: scripted in-cluster serve check (port-forward + curl `/healthz`, `/api/stream`) replacing the manual Phase 2 run; fold into Phase 7's install e2e (added 2026-11-10, Phase 2 review finding 2).
- Static control-plane pods on kind show as system apps beside the inferred `cp/*` bubbles; decide in Phase 3 whether to fold them (added 2026-11-10, Phase 2 live check).
