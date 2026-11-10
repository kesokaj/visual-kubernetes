# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Phase 3 — Spider-web page shell · PLAN-2026-11-09-1906-plan-visual-kubernetes-v1-live-spider-web-visual · goal: the embedded page draws the live web from `/api/stream` (and `--replay` fixtures). Phase 2 done 2026-11-10.

- [ ] `web/` TanStack Start SPA embedded via `go:embed`
- [ ] Official Kubernetes icons vendored with their licence
- [ ] Force web: SVG + d3-force, app bubbles with pod dots, API hub, nodes, threads; auto-fit, zoom, pan, F
- [ ] Filter pill: 8 groups, all on by default, V hides it
- [ ] Checkpoint

## Next

- Phase 4 — Panel, encodings, folding and performance.
- Phase 5 — Teaching replay and scenarios.
- Phase 6 — Hubble traffic.

## Backlog

- Size-metric switch (PRD C3; added 2026-11-09, plan D11).
- CRD kinds outside the named RBAC groups (added 2026-11-09, plan D16).
- OTLP receiver as a traffic source (added 2026-11-09, grill N4/row 6).
- Cloud Audit Logs stream for real API-check steps (added 2026-11-09, grill Q7).
- Elliptical, label-aware spacing to stop label overlaps (added 2026-11-09, mockup v3 nit).
- Watch Events from one API (core/v1 and events.k8s.io/v1 are both cached) and trim them to the streamed fields; deferred from Phase 2 because it edits `internal/watch`, outside that phase's files (added 2026-11-10, security review 2026-11-10 finding 1).
- `test/kind/serve.sh`: scripted in-cluster serve check (port-forward + curl `/healthz`, `/api/stream`) replacing the manual Phase 2 run; fold into Phase 7's install e2e (added 2026-11-10, Phase 2 review finding 2).
- Static control-plane pods on kind show as system apps beside the inferred `cp/*` bubbles; decide in Phase 3 whether to fold them (added 2026-11-10, Phase 2 live check).
