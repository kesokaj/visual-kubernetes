#!/usr/bin/env bash
# Creates the kind test cluster, approves the kubelet-serving CSRs that serverTLSBootstrap
# leaves pending, and installs metrics-server. The kubeconfig is written under
# .agents/scratch/kind/ so the operator's ~/.kube/config and current context are never touched.
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cluster="visual-kubernetes"
export KUBECONFIG="${KIND_KUBECONFIG:-$repo/.agents/scratch/kind/kubeconfig}"
metrics_server_version="${METRICS_SERVER_VERSION:-v0.8.0}"
nodes=3

mkdir -p "$(dirname "$KUBECONFIG")"

if kind get clusters 2>/dev/null | grep -qx "$cluster"; then
  echo "cluster $cluster exists; refreshing kubeconfig"
  kind export kubeconfig --name "$cluster" --kubeconfig "$KUBECONFIG"
else
  kind create cluster --config "$repo/test/kind/config.yaml" --kubeconfig "$KUBECONFIG" --wait 120s
fi

# Each kubelet files one kubernetes.io/kubelet-serving CSR; approve until every node has one issued.
approve_csrs() {
  local pending
  pending="$(kubectl get csr -o go-template='{{range .items}}{{if and (eq .spec.signerName "kubernetes.io/kubelet-serving") (not .status.conditions)}}{{.metadata.name}}{{"\n"}}{{end}}{{end}}')"
  if [[ -n "$pending" ]]; then
    # shellcheck disable=SC2086 # one CSR name per word
    kubectl certificate approve $pending
  fi
}

issued_count() {
  kubectl get csr -o go-template='{{range .items}}{{if and (eq .spec.signerName "kubernetes.io/kubelet-serving") .status.certificate}}{{.spec.username}}{{"\n"}}{{end}}{{end}}' | sort -u | grep -c . || true
}

for _ in $(seq 1 60); do
  approve_csrs
  if [[ "$(issued_count)" -ge "$nodes" ]]; then
    break
  fi
  sleep 2
done
if [[ "$(issued_count)" -lt "$nodes" ]]; then
  echo "error: only $(issued_count) of $nodes kubelets have an issued serving cert" >&2
  exit 1
fi

# metrics-server verifies kubelet certs against the cluster CA, so no --kubelet-insecure-tls.
kubectl apply -f "https://github.com/kubernetes-sigs/metrics-server/releases/download/${metrics_server_version}/components.yaml"
kubectl -n kube-system rollout status deployment/metrics-server --timeout=180s

echo "ready: KUBECONFIG=$KUBECONFIG"
