#!/usr/bin/env bash
# Builds the image, side-loads it into the kind cluster from up.sh, runs the probe once as a Pod
# under the planned read-only ClusterRole, and saves its output to .agents/scratch/probe/.
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cluster="visual-kubernetes"
image="visual-kubernetes:dev"
export KUBECONFIG="${KIND_KUBECONFIG:-$repo/.agents/scratch/kind/kubeconfig}"
out="$repo/.agents/scratch/probe"
mkdir -p "$out"

docker build -t "$image" "$repo"
kind load docker-image "$image" --name "$cluster"

kubectl apply -f "$repo/test/kind/probe-rbac.yaml"
kubectl -n visual-kubernetes delete pod visual-kubernetes-probe --ignore-not-found --wait=true
kubectl apply -f "$repo/test/kind/probe-pod.yaml"
# Wait for either terminal phase so a failed probe saves its logs instead of timing out silently.
phase=""
for _ in $(seq 1 60); do
  phase="$(kubectl -n visual-kubernetes get pod visual-kubernetes-probe -o jsonpath='{.status.phase}')"
  [[ "$phase" == Succeeded || "$phase" == Failed ]] && break
  sleep 2
done

kubectl -n visual-kubernetes logs pod/visual-kubernetes-probe -c probe >"$out/in-cluster.log" 2>&1 || true
# The probe prints its result lines on stdout and JSON logs on stderr; keep only result lines.
grep -v '^{' "$out/in-cluster.log" | tee "$out/in-cluster.txt" || true
if [[ "$phase" != Succeeded ]]; then
  echo "error: probe pod phase '$phase'; see $out/in-cluster.log" >&2
  exit 1
fi
