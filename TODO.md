# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Phase 6 — Hubble traffic · PLAN-2026-11-09-1906-plan-visual-kubernetes-v1-live-spider-web-visual · goal: flows from Hubble Relay drawn as particles along edges, red for policy drops. Phase 5 done 2026-11-11 (chains in recorded streams only until D48 is approved).

- [ ] Hubble client: auto-detect Cilium and GKE DPv2 Hubble Relay
- [ ] Canvas 2D particle overlay along edges, red for policy drops
- [ ] `test/kind/up.sh --cilium` and `examples/04-traffic`
- [ ] Checkpoint

## Next

- Phase 7 — Packaging, install and live e2e, plus `deploy/install-lb.yaml`, the `--logs` flag and `TestInstallLBRequiresSourceRanges` (ADR-008).
- `/fyr-preflight` for v1 and the LB deploy on the operator's go (plan D41).

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
- Live causal-chain wiring: `replay.Builder` on the serve.go watch pump, plus keeping the newest managedFields manager in `internal/watch` trimForCache (added 2026-11-11, plan D48, REVIEW phase 5 r1 finding 1; needs the operator to approve `cmd/` + `internal/watch/` files).
- `examples/*.sh` context guard (refuse unless the context is named or confirmed); today they print the context and the README warns (added 2026-11-11, security phase 5 finding 1).
