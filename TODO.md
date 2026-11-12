# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

### Phase 2 — Workload environment variable editor (`PLAN-2026-11-12-1113-plan-interactive-demo-mode-controlled-proxy-work`)

Goal: Let presenters in `--demo` mode inspect and update plain container env vars on `Deployment`, `StatefulSet`, and `DaemonSet` workloads (`409` conflict check; `valueFrom` Secret/ConfigMap/field refs preserved read-only; system namespaces `403`).

- [ ] `GET` & `PATCH /api/demo/workloads/env` for `Deployment`, `StatefulSet`, `DaemonSet` (plain `value` set/remove, `valueFrom` read-only preservation, `resourceVersion` `409`, protected-ns `403`, replay fake)
- [ ] Panel env editor + `test/e2e/demo-env.spec.ts` (edit, add, Secret-backed var locked, stale `409` notice)
- [ ] Checkpoint

## Next

- Phase 3 — Interactive pod exec terminal & live local `kind` demo verification (`PLAN-2026-11-12-1113-plan-interactive-demo-mode-controlled-proxy-work`)
- Phase 4 — Google IAP authentication, `--allowed-host`, and `deploy/install-iap.yaml` (`PLAN-2026-11-12-1113-plan-interactive-demo-mode-controlled-proxy-work`)
- Size-metric switch (`network | CPU | memory | pods`, `PRD.md` `C3`)

## Backlog

- ~~Globe drawn when its only entrypoint targets are hidden~~ fixed: `scene.ts` `pruneGlobe` runs under every filter (2026-11-11, review DoD finding 3).
- ~~`interact.spec.ts:101` zoom flake~~ fixed: baseline read after `settle()` (2026-11-11, verifier root cause).
- Verify whether GKE DPv2 Hubble fills `destination_service` (kind Cilium leaves it null, so via is inferred; added 2026-11-12, plan D9).
- Size-metric switch (PRD C3; added 2026-11-09, plan D11; queued next after `PLAN-2026-11-12-1113`).
- CRD kinds outside the named RBAC groups (added 2026-11-09, plan D16).
- OTLP receiver as a traffic source (added 2026-11-09, grill N4/row 6).
- Cloud Audit Logs stream for real API-check steps (added 2026-11-09, grill Q7).
- ~~Elliptical, label-aware spacing to stop label overlaps~~ done via label-aware collision clearance and airier force distances (2026-11-11, plan D54).
- ~~`test/kind/serve.sh`~~ done as `test/kind/install-check.sh` (2026-11-10, preflight fixes) and `test/e2e/live.sh` / `test/e2e/live.spec.ts` (2026-11-11, Phase 7).
- ~~`--allowed-host` flag so a DNS name in front of the LoadBalancer passes the Host guard~~ scheduled in `PLAN-2026-11-12-1113` Phase 4 (2026-11-12, ADR-011).
- ~~IAP or an authenticated Ingress in front of the UI, for v2~~ scheduled in `PLAN-2026-11-12-1113` Phase 4 (2026-11-12, ADR-011).
- Optional egress NetworkPolicy examples per CNI in the README (API server, kubelets, Hubble Relay); not in the default install, which must stay portable (added 2026-11-10, REVIEW-2026-11-10-1559-security-round-2 finding 1).
- Live causal-chain field-manager names: `replay.Builder` is wired in `cmd/visual-kubernetes/serve.go` (`newChainSink`, 2026-11-11, plan D55) using fallback actor `"client"`; keeping the newest `managedFields` manager in `internal/watch` `trimForCache` remains optional (added 2026-11-11, plan D48).
- `examples/*.sh` context guard (refuse unless the context is named or confirmed); today they print the context and the README warns (added 2026-11-11, security phase 5 finding 1).
