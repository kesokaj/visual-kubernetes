# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Phase 0 — Spike, kubelet client and capability probe · PLAN-2026-11-09-1906-plan-visual-kubernetes-v1-live-spider-web-visual · goal: prove which built-in sources the in-cluster binary can read on kind, with kubelet TLS verified.

- [ ] kind installed on Colima
- [ ] `test/kind/config.yaml` + `test/kind/up.sh` (serverTLSBootstrap, CSR approval, metrics-server)
- [ ] `internal/kubelet` Summary client with `TestClientRefusesUnverifiedCert` (R8)
- [ ] `probe` subcommand, one probe per source
- [ ] Minimal Dockerfile; probe run in-cluster on kind
- [ ] Four ADRs (SSE, rendering, force web, kubelet direct)
- [ ] GUIDE-testing filled
- [ ] Checkpoint

## Next

- Phase 1 — Model and watches.
- Phase 2 — Sources and SSE stream.
- Phase 3 — Spider-web page shell.

## Backlog

- Size-metric switch (PRD C3; added 2026-11-09, plan D11).
- CRD kinds outside the named RBAC groups (added 2026-11-09, plan D16).
- OTLP receiver as a traffic source (added 2026-11-09, grill N4/row 6).
- Cloud Audit Logs stream for real API-check steps (added 2026-11-09, grill Q7).
- Elliptical, label-aware spacing to stop label overlaps (added 2026-11-09, mockup v3 nit).
