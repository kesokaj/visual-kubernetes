# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

## Next

- Size-metric switch (`network | CPU | memory | pods`, `PRD.md` `C3`)

## Backlog

- Optional persistence of open `--demo` proxy port toggles across restarts (added 2026-11-12, `PLAN-2026-11-12-1113` follow-up).
- ~~Globe drawn when its only entrypoint targets are hidden~~ fixed: `scene.ts` `pruneGlobe` runs under every filter (2026-11-11, review DoD finding 3).
- ~~`interact.spec.ts:101` zoom flake~~ fixed: baseline read after `settle()` (2026-11-11, verifier root cause).
- Verify whether GKE DPv2 Hubble fills `destination_service` (kind Cilium leaves it null, so via is inferred; added 2026-11-12, plan D9).
- CRD kinds outside the named RBAC groups (added 2026-11-09, plan D16).
- OTLP receiver as a traffic source (added 2026-11-09, grill N4/row 6).
- Cloud Audit Logs stream for real API-check steps (added 2026-11-09, grill Q7).
- ~~Elliptical, label-aware spacing to stop label overlaps~~ done via label-aware collision clearance and airier force distances (2026-11-11, plan D54).
- ~~`test/kind/serve.sh`~~ done as `test/kind/install-check.sh` (2026-11-10, preflight fixes) and `test/e2e/live.sh` / `test/e2e/live.spec.ts` (2026-11-11, Phase 7).
- ~~`--allowed-host` flag so a DNS name in front of the LoadBalancer passes the Host guard~~ done in `PLAN-2026-11-12-1113` Phase 4 (2026-11-12, ADR-011).
- ~~IAP or an authenticated Ingress in front of the UI, for v2~~ done in `PLAN-2026-11-12-1113` Phase 4 (2026-11-12, ADR-011).
- Optional egress NetworkPolicy examples per CNI in the README (API server, kubelets, Hubble Relay); not in the default install, which must stay portable (added 2026-11-10, REVIEW-2026-11-10-1559-security-round-2 finding 1).
- Live causal-chain field-manager names: `replay.Builder` is wired in `cmd/visual-kubernetes/serve.go` (`newChainSink`, 2026-11-11, plan D55) using fallback actor `"client"`; keeping the newest `managedFields` manager in `internal/watch` `trimForCache` remains optional (added 2026-11-11, plan D48).
- `examples/*.sh` context guard (refuse unless the context is named or confirmed); today they print the context and the README warns (added 2026-11-11, security phase 5 finding 1).
