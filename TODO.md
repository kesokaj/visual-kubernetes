# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Phase 2 — Sources and SSE stream · PLAN-2026-11-09-1906-plan-visual-kubernetes-v1-live-spider-web-visual · goal: metrics sources and the SSE endpoint serve the live model (and `--replay` fixtures) to curl. Phase 1 done 2026-11-10.

- [ ] Metrics sources: metrics.k8s.io, kubelet Summary network bytes (hostNetwork excluded), `/metrics`, `/readyz`, Leases → `cp/*`
- [ ] SSE endpoint (snapshot, deltas, Last-Event-ID, 15 min ring), `/healthz`, `/api/objects`, `/api/logs`, `--replay FILE`
- [ ] Live check on kind (in-cluster Deployment, port-forward + curl)
- [ ] Checkpoint

## Next

- Phase 3 — Spider-web page shell.
- Phase 4 — Panel, encodings, folding and performance.
- Phase 5 — Teaching replay and scenarios.

## Backlog

- Size-metric switch (PRD C3; added 2026-11-09, plan D11).
- CRD kinds outside the named RBAC groups (added 2026-11-09, plan D16).
- OTLP receiver as a traffic source (added 2026-11-09, grill N4/row 6).
- Cloud Audit Logs stream for real API-check steps (added 2026-11-09, grill Q7).
- Elliptical, label-aware spacing to stop label overlaps (added 2026-11-09, mockup v3 nit).
- Watch Events from one API (core/v1 and events.k8s.io/v1 are both cached) and trim them to the streamed fields; do with Phase 2 (added 2026-11-10, security review 2026-11-10 finding 1).
