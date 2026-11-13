# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

### Phase 2 — Click-panel UI editors, `ExecSection` clarity, Playwright e2e, live kind smoke, and docs (`PLAN-2026-11-13-1117-plan-demo-panel-controls-for-service-type-worklo`)
Goal: ship click-panel UI controls (`ServiceTypeSection`, `ScaleSection`, `AutoscalerSection`), `ExecSection` status clarity and `Service`/`Node` pod launch chips, offline Playwright e2e (`demo-config.spec.ts` + `demo-exec.spec.ts`), live `kind` smoke checks (`demo-smoke.sh`), and documentation updates.

- [ ] Add typed client helpers (`web/src/demo/api.ts`), `ServiceTypeSection.tsx` (`useServiceType.ts`), `ScaleSection.tsx` (`useWorkloadScale.ts`), `AutoscalerSection.tsx` (`useAutoscaler.ts`), and `ExecSection.tsx` status/satellite/node improvements, wired into `web/src/panel/Panel.tsx` and `ProxySection.tsx` refresh/`BrowserSheet` close — verify: build, `pnpm -C web build && go build ./...`, who: AI — expect: exit 0, TypeScript + Vite bundle clean
- [ ] Extend `test/e2e/fixtures/demo.mjs` (adding `default/kubernetes` Service, HPA on `shop/web`, VPA on `shop/worker`, `0`-replica `shop/idle` Deployment), update `test/e2e/demo-exec.spec.ts`, and add `test/e2e/demo-config.spec.ts` covering golden paths, `403`/`409` edge cases, R9 and R10 (validated in must-fail state) — verify: e2e, `pnpm -C web exec playwright test demo-config.spec.ts demo-exec.spec.ts demo-proxy.spec.ts demo-env.spec.ts`, who: AI — expect: all specs pass, 0 text < 12px, 0 foreign requests
- [ ] Extend `test/kind/demo-smoke.sh` with live `Service` `ClusterIP` → `LoadBalancer` → proxy `200` → `ClusterIP` (`403`), `Deployment` scale, `HPA` patch, and `default/kubernetes` `403` checks, and update `AGENTS.md`, `README.md`, `SDD.md`, `TDD.md`, and `GUIDE-testing` — verify: integration, `test/kind/up.sh && test/kind/demo-smoke.sh`, who: AI — expect: `demo-smoke: PASS` — docs: AGENTS.md, README.md, SDD.md, TDD.md, GUIDE-testing
- [ ] Checkpoint — expect: full Go race suite, full offline Playwright suite, live kind smoke, and `fyr check` pass — evidence: `go vet ./... && go test -race ./...`, `pnpm -C web build && pnpm -C web exec playwright test`, `test/kind/demo-smoke.sh`, `fyr check` exit 0

## Next

<!-- none -->

## Backlog

- ~~Size-metric switch (`network | CPU | memory | pods`, `PRD.md` `C3`)~~ done: filter pill `Size by` switch (2026-11-12, `size.spec.ts`).
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
- ~~`--demo` exec: also refuse `hostPID`/`hostIPC`/privileged Pods~~ done: `KubeExec.ResolvePod` (`checkPodIsolation`) refuses `hostNetwork`, `hostPID`, `hostIPC`, `hostPath`, and privileged or non-baseline-capability `containers`/`initContainers`/`ephemeralContainers` with `403` (2026-11-13, `exec_isolation_test.go`).
