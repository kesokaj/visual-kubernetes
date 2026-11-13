# TODO

<!-- Now is the active phase and mirrors its plan record's boxes, updated in the commit that ticks them; on a
     conflict the plan record wins. Next holds at most three phases, one line each. Backlog holds unscheduled
     ideas, one line each, dated, with their source. An action only the operator can take goes in the newest
     handoff's Next Steps. -->

## Now

### Phase 1 — `--demo` RBAC manifests and Service / Scale / Autoscaler Go & replay backends (`PLAN-2026-11-13-1117-plan-demo-panel-controls-for-service-type-worklo`)
Goal: widen `install-demo.yaml` and `install-iap.yaml` RBAC with bidirectional tests and implement guarded live + `--replay` stores and `/api/demo/*` routes for `Service` `ClusterIP`/`LoadBalancer` type, workload `replicas`, and `HPA`/`VPA` scaling.

- [ ] Update `deploy/install-demo.yaml` and `deploy/install-iap.yaml` ClusterRole rules with narrow `patch, update` on `""` `services`, `autoscaling` `horizontalpodautoscalers`, and `autoscaling.k8s.io` `verticalpodautoscalers`, and pin both ways in `deploy/demo_checks_test.go` and `deploy/iap_checks_test.go` — verify: unit, `go test -race ./deploy/...`, who: AI — expect: exit 0; read-only manifests stay unchanged and mutated extra/missing rules fail
- [ ] Implement `ServiceStore` (`internal/demo/{service,service_k8s,replay_service,service_test}.go`), `Catalog.IsServiceRouted` + `SetServiceTypeOverride`, `gate.revokeService`, and `GET`/`PATCH /api/demo/services/type` — verify: unit, `go test -race ./internal/demo/... -run TestServiceType`, who: AI — expect: exit 0 (`ClusterIP` ↔ `LoadBalancer`, `healthCheckNodePort`/`nodePort` cleared, immediate proxy revocation without watch race, replay `ext/internet` `upsert`/`delete` deltas, `default/kubernetes` & `kube-system` `403`, headless/ExternalName `400`, stale `rv` `409`)
- [ ] Extend `WorkloadStore` with `GetWorkloadScale`/`PatchWorkloadScale` (`internal/demo/{scale,scale_k8s,scale_test}.go` + `ReplayWorkloads`) and `GET`/`PATCH /api/demo/workloads/scale` — verify: unit, `go test -race ./internal/demo/... -run TestWorkloadScale`, who: AI — expect: exit 0 (`Deployment` & `StatefulSet` `0..50`, `managedByHPA` populated, `DaemonSet` & out-of-range `400`, `409` conflict, `kube-system` `403`)
- [ ] Implement `AutoscalerStore` (`internal/demo/{autoscaler,autoscaler_k8s,replay_autoscaler,autoscaler_test}.go`) + `GET`/`PATCH /api/demo/autoscalers` and wire all stores in `cmd/visual-kubernetes/serve.go` + `cmd/visual-kubernetes/serve_demo_test.go` — verify: unit, `go test -race ./internal/demo/... ./cmd/visual-kubernetes/...`, who: AI — expect: exit 0 (HPA `min/max` & VPA unstructured nil/wildcard/clear-key handling + missing CRD `404` + replay parity + `--demo=false` `403`)
- [ ] Checkpoint — expect: all Go packages pass vet and race tests cleanly — evidence: `go vet ./... && go test -race ./...` exit 0

## Next

- Phase 2 (`PLAN-2026-11-13-1117-plan-demo-panel-controls-for-service-type-worklo`): Click-panel UI editors (`ServiceTypeSection`, `ScaleSection`, `AutoscalerSection`), `ExecSection` clarity & `Service`/`Node` pod chips, Playwright e2e, live `kind` smoke, and docs.

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
