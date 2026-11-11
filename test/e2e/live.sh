#!/usr/bin/env bash
# End-to-end live verification on kind (plan Phase 7, Box 7.5):
#   1. Ensures the local kind cluster `visual-kubernetes` is up (never touches ~/.kube/config)
#   2. Builds the web bundle + container image and loads it into kind (unless VK_LIVE_USE_MANIFEST_IMAGE=1)
#   3. Applies deploy/install.yaml, waits for rollout, and port-forwards svc/visual-kubernetes
#   4. Runs the ServiceAccount `kubectl auth can-i --list` read-only check and Playwright `live.spec.ts`
#      (golden path + G2 p95 <= 2 s over 10 scales + live R/Space/-> replay, filter, and missing metrics-server)
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo
readonly cluster="visual-kubernetes"
readonly ns="visual-kubernetes"
readonly test_ns="vk-live"
readonly port="${VK_LIVE_PORT:-18095}"

out="$repo/.agents/scratch/e2e/live"
mkdir -p "$out"

if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
  echo "error: 127.0.0.1:$port is already bound; set VK_LIVE_PORT to a free port" >&2
  exit 1
fi

if [[ "${VK_ALLOW_NON_KIND:-0}" == 1 ]]; then
  export KUBECONFIG="${KIND_KUBECONFIG:-${KUBECONFIG:-$repo/.agents/scratch/kind/kubeconfig}}"
  ctx="$(kubectl config current-context)"
else
  export KUBECONFIG="${KIND_KUBECONFIG:-$repo/.agents/scratch/kind/kubeconfig}"
  if [[ -f "$KUBECONFIG" ]] && ctx="$(kubectl config current-context 2>/dev/null)" && [[ "$ctx" != "kind-$cluster" ]]; then
    echo "error: refusing to run live.sh against non-kind context '$ctx' (expected kind-$cluster)" >&2
    exit 2
  fi
  if ! kind get clusters 2>/dev/null | grep -qx "$cluster"; then
    "$repo/test/kind/up.sh"
  fi
  ctx="$(kubectl config current-context)"
  if [[ "$ctx" != "kind-$cluster" ]]; then
    echo "error: refusing to run live.sh against non-kind context '$ctx' (expected kind-$cluster)" >&2
    exit 2
  fi
fi

pf_pid=""
cleanup() {
  if [[ -n "$pf_pid" ]]; then
    kill "$pf_pid" 2>/dev/null || true
  fi
  if [[ -f "$out/metrics-apiservice.json" ]] && ! kubectl get apiservice v1beta1.metrics.k8s.io >/dev/null 2>&1; then
    kubectl apply -f "$out/metrics-apiservice.json" >/dev/null 2>&1 ||
      echo "WARN: failed to restore apiservice v1beta1.metrics.k8s.io from $out/metrics-apiservice.json" >&2
  fi
  kubectl delete namespace "$test_ns" --ignore-not-found --wait=false >/dev/null 2>&1 || true
}
trap cleanup EXIT

if [[ "${VK_LIVE_USE_MANIFEST_IMAGE:-0}" != 1 ]]; then
  pnpm -C "$repo/web" build
  docker build -t visual-kubernetes:dev "$repo"
  kind load docker-image visual-kubernetes:dev --name "$cluster"
fi

kubectl apply -f "$repo/deploy/install.yaml"
if [[ "${VK_LIVE_USE_MANIFEST_IMAGE:-0}" != 1 ]]; then
  kubectl -n "$ns" set image deploy/visual-kubernetes visual-kubernetes=visual-kubernetes:dev
  kubectl -n "$ns" rollout restart deploy/visual-kubernetes
fi
kubectl -n "$ns" rollout status deploy/visual-kubernetes --timeout=180s

# Save APIService v1beta1.metrics.k8s.io before Playwright runs so cleanup can restore it on interrupt.
if kubectl get apiservice v1beta1.metrics.k8s.io >/dev/null 2>&1; then
  node -e '
    const { execFileSync } = require("node:child_process");
    const fs = require("node:fs");
    const raw = JSON.parse(execFileSync("kubectl", ["get", "apiservice", "v1beta1.metrics.k8s.io", "-o", "json"], { encoding: "utf8" }));
    delete raw.metadata.resourceVersion;
    delete raw.metadata.uid;
    delete raw.metadata.creationTimestamp;
    delete raw.metadata.managedFields;
    delete raw.status;
    fs.writeFileSync(process.argv[1], JSON.stringify(raw, null, 2));
  ' "$out/metrics-apiservice.json"
fi

kubectl -n "$ns" port-forward svc/visual-kubernetes "$port:80" >"$out/port-forward.log" 2>&1 &
pf_pid=$!

ready=0
for _ in $(seq 1 60); do
  if ! kill -0 "$pf_pid" 2>/dev/null; then
    echo "error: kubectl port-forward exited early:" >&2
    cat "$out/port-forward.log" >&2
    exit 1
  fi
  if curl -fsS "http://127.0.0.1:$port/healthz" >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 0.5
done
if [[ "$ready" != 1 ]]; then
  echo "error: http://127.0.0.1:$port/healthz did not become ready" >&2
  cat "$out/port-forward.log" >&2
  exit 1
fi

sa="system:serviceaccount:$ns:visual-kubernetes"
kubectl auth can-i --list --as="$sa" >"$out/can-i-list.txt"

VK_LIVE_URL="http://127.0.0.1:$port" \
VK_LIVE_SCRATCH="$out" \
VK_REPO_ROOT="$repo" \
KUBECONFIG="$KUBECONFIG" \
  pnpm -C "$repo/web" exec playwright test live.spec.ts

echo "live e2e: PASS ($ctx)"
