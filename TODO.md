# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Phase 5 — Teaching replay and scenarios · PLAN-2026-11-09-1906-plan-visual-kubernetes-v1-live-spider-web-visual · goal: causal chains, replay keys, example scenarios, G3-scale perf probe. Phase 4 done 2026-11-10 (box 4.5 reworded and ticked by D40). The Events Task (plan D29 + D32) is done (Session 2, 2026-11-10).

- [ ] Causal chain builder (D17) emitting `chain` events
- [ ] Replay keys: Space pause, → step, R replay the last chain
- [ ] `examples/01-deployment`, `02-nodes`, `03-hpa`, `03b-vpa`, each with a short script
- [ ] Perf probe at G3 scale: a 150-bubble / 1,000-pod fixture (D42)
- [ ] Checkpoint

## Next

- Phase 6 — Hubble traffic.
- Phase 7 — Packaging, install and live e2e, plus `deploy/install-lb.yaml`, the `--logs` flag and `TestInstallLBRequiresSourceRanges` (ADR-008).

## Backlog

- Size-metric switch (PRD C3; added 2026-11-09, plan D11).
- CRD kinds outside the named RBAC groups (added 2026-11-09, plan D16).
- OTLP receiver as a traffic source (added 2026-11-09, grill N4/row 6).
- Cloud Audit Logs stream for real API-check steps (added 2026-11-09, grill Q7).
- Elliptical, label-aware spacing to stop label overlaps (added 2026-11-09, mockup v3 nit).
- ~~`test/kind/serve.sh`~~ done as `test/kind/install-check.sh` (2026-11-10, preflight fixes); the Playwright `live.spec.ts` stays a Phase 7 box.
- `--allowed-host` flag so a DNS name in front of the LoadBalancer passes the Host guard; not in v1 (added 2026-11-10, ADR-008 decision F, plan D39).
- IAP or an authenticated Ingress in front of the UI, for v2 (added 2026-11-10, ADR-008 rejected options).
- Optional egress NetworkPolicy examples per CNI in the README (API server, kubelets, Hubble Relay); not in the default install, which must stay portable (added 2026-11-10, REVIEW-2026-11-10-1559-security-round-2 finding 1).
