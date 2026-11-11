#!/usr/bin/env bash
# Example 03b: a VerticalPodAutoscaler raises the requests of pods that asked for too little.
# Needs the VPA CRDs and controllers; this script checks for them and never installs them.
# Uses your current kubectl context unless KUBECONFIG says otherwise; NS defaults to vk-demo.
#   examples/03b-vpa.sh up | down
set -euo pipefail

dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly dir
readonly ns="${NS:-vk-demo}"

echo "context: $(kubectl config current-context)  namespace: $ns"
case "${1:-}" in
  up)
    if ! kubectl get crd verticalpodautoscalers.autoscaling.k8s.io >/dev/null 2>&1; then
      echo "this cluster has no VerticalPodAutoscaler CRD; see examples/README.md (03b)" >&2
      exit 1
    fi
    kubectl create namespace "$ns" --dry-run=client -o yaml | kubectl apply -f -
    kubectl -n "$ns" apply -f "$dir/03b-vpa.yaml"
    echo "the recommender needs a few minutes; then: kubectl -n $ns describe vpa hungry"
    ;;
  down)
    kubectl -n "$ns" delete -f "$dir/03b-vpa.yaml" --ignore-not-found
    ;;
  *)
    echo "usage: $0 up | down" >&2
    exit 2
    ;;
esac
