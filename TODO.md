# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

Phase 7 — Packaging, install and live e2e · PLAN-2026-11-09-1906-plan-visual-kubernetes-v1-live-spider-web-visual · goal: finalize `DEPLOY-local`/`DEPLOY-gcp`, `deploy/install-kube-system.yaml`, `--logs` + `deploy/install-lb.yaml` (ADR-008), `test/e2e/live.sh` + `live.spec.ts` (G2 p95 ≤ 2 s), and `fyr check` exit 0. Phase 6 done 2026-11-11.

- [ ] Dockerfile finalized + `DEPLOY-local` check (multi-arch and `--replay` smoke already done in v0.1.0)
- [ ] `deploy/install.yaml` (done in v0.1.0) and `deploy/install-kube-system.yaml`
- [ ] `--logs` flag (D38, default true; `--logs=false` → 403 on `/api/logs` + panel note)
- [ ] `deploy/install-lb.yaml` + `TestInstallLBRequiresSourceRanges` (ADR-008, D37, D47)
- [ ] Live e2e on kind (`test/e2e/live.sh` + `test/e2e/live.spec.ts`, G2 p95 ≤ 2 s)
- [ ] `DEPLOY-gcp` updated with `install-lb.yaml` release step
- [ ] Checkpoint (`go test -race ./...`, Playwright, `test/e2e/live.sh`, and `fyr check` exit 0 after D43 PRD `enforced by:` approval)

## Next

- `/fyr-preflight` for v1 and the LB deploy on the operator's go (plan D41).

## Backlog

- Size-metric switch (PRD C3; added 2026-11-09, plan D11).
- CRD kinds outside the named RBAC groups (added 2026-11-09, plan D16).
- OTLP receiver as a traffic source (added 2026-11-09, grill N4/row 6).
- Cloud Audit Logs stream for real API-check steps (added 2026-11-09, grill Q7).
- ~~Elliptical, label-aware spacing to stop label overlaps~~ done via label-aware collision clearance and airier force distances (2026-11-11, plan D54).
- ~~`test/kind/serve.sh`~~ done as `test/kind/install-check.sh` (2026-11-10, preflight fixes); the Playwright `live.spec.ts` stays a Phase 7 box.
- `--allowed-host` flag so a DNS name in front of the LoadBalancer passes the Host guard; not in v1 (added 2026-11-10, ADR-008 decision F, plan D39).
- IAP or an authenticated Ingress in front of the UI, for v2 (added 2026-11-10, ADR-008 rejected options).
- Optional egress NetworkPolicy examples per CNI in the README (API server, kubelets, Hubble Relay); not in the default install, which must stay portable (added 2026-11-10, REVIEW-2026-11-10-1559-security-round-2 finding 1).
- Live causal-chain wiring: `replay.Builder` on the serve.go watch pump, plus keeping the newest managedFields manager in `internal/watch` trimForCache (added 2026-11-11, plan D48, REVIEW phase 5 r1 finding 1; needs the operator to approve `cmd/` + `internal/watch/` files).
- `examples/*.sh` context guard (refuse unless the context is named or confirmed); today they print the context and the README warns (added 2026-11-11, security phase 5 finding 1).
