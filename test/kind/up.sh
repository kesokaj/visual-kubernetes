#!/usr/bin/env bash
# Creates the kind test cluster, approves the kubelet-serving CSRs that serverTLSBootstrap
# leaves pending, and installs metrics-server. With --cilium, disables default kindnet and
# installs Cilium + Hubble Relay via Helm so /api/stream integration tests exercise live flows.
# The kubeconfig is written under .agents/scratch/kind/ so the operator's ~/.kube/config and
# current context are never touched.
set -euo pipefail

cilium=0
case "${1:-}" in
  "") ;;
  --cilium) cilium=1 ;;
  *)
    echo "usage: $0 [--cilium]" >&2
    exit 2
    ;;
esac
if [[ $# -gt 1 ]]; then
  echo "usage: $0 [--cilium]" >&2
  exit 2
fi
if [[ "$cilium" == 1 ]] && ! command -v helm >/dev/null 2>&1; then
  echo "error: --cilium requires helm in PATH" >&2
  exit 2
fi

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo
readonly cluster="visual-kubernetes"
export KUBECONFIG="${KIND_KUBECONFIG:-$repo/.agents/scratch/kind/kubeconfig}"
readonly metrics_server_version="${METRICS_SERVER_VERSION:-v0.8.0}"
readonly cilium_version="${CILIUM_VERSION:-1.17.2}"
readonly nodes=3

mkdir -p "$(dirname "$KUBECONFIG")"

if kind get clusters 2>/dev/null | grep -qx "$cluster"; then
  kind export kubeconfig --name "$cluster" --kubeconfig "$KUBECONFIG"
  if [[ "$cilium" == 1 ]] && ! kubectl -n kube-system get daemonset cilium >/dev/null 2>&1; then
    echo "cluster $cluster has no Cilium; recreating with config-cilium.yaml"
    kind delete cluster --name "$cluster"
  else
    echo "cluster $cluster exists; refreshing kubeconfig"
  fi
fi

if ! kind get clusters 2>/dev/null | grep -qx "$cluster"; then
  if [[ "$cilium" == 1 ]]; then
    # Nodes stay NotReady until Cilium initializes CNI, so omit --wait until Cilium is installed.
    kind create cluster --config "$repo/test/kind/config-cilium.yaml" --kubeconfig "$KUBECONFIG"
  else
    kind create cluster --config "$repo/test/kind/config.yaml" --kubeconfig "$KUBECONFIG" --wait 120s
  fi
fi

if [[ "$cilium" == 1 ]]; then
  helm upgrade --install cilium cilium \
    --repo https://helm.cilium.io/ \
    --version "$cilium_version" \
    --namespace kube-system \
    --set image.pullPolicy=IfNotPresent \
    --set ipam.mode=kubernetes \
    --set kubeProxyReplacement=false \
    --set operator.replicas=1 \
    --set hubble.enabled=true \
    --set hubble.relay.enabled=true
  kubectl -n kube-system rollout status daemonset/cilium --timeout=240s
  kubectl -n kube-system rollout status deployment/cilium-operator --timeout=180s
  kubectl wait --for=condition=Ready nodes --all --timeout=180s
  kubectl -n kube-system rollout status deployment/coredns --timeout=180s
  kubectl -n kube-system rollout status deployment/hubble-relay --timeout=180s
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
