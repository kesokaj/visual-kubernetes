# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Phase 1 — Model and watches · PLAN-2026-11-09-1906-plan-visual-kubernetes-v1-live-spider-web-visual · goal: informers feed an in-memory model (entity ids, apps, satellites, folding) and fixturegen writes the four replay streams. Phase 0 done 2026-11-09.

- [ ] Informers for the named groups (Secrets excluded, forbidden kinds skipped) feeding `internal/model`
- [ ] Model: entity ids, app grouping and satellites, filterGroup, edges, `counter/*` folding
- [ ] `test/fixturegen` writes the four `test/fixtures/streams/*.ndjson`, byte-identical on rerun
- [ ] Checkpoint

## Next

- Phase 2 — Sources and SSE stream.
- Phase 3 — Spider-web page shell.
- Phase 4 — Panel, encodings, folding and performance.

## Backlog

- Size-metric switch (PRD C3; added 2026-11-09, plan D11).
- CRD kinds outside the named RBAC groups (added 2026-11-09, plan D16).
- OTLP receiver as a traffic source (added 2026-11-09, grill N4/row 6).
- Cloud Audit Logs stream for real API-check steps (added 2026-11-09, grill Q7).
- Elliptical, label-aware spacing to stop label overlaps (added 2026-11-09, mockup v3 nit).
