#!/usr/bin/env bash
# Example 01: create a Deployment, scale it, remove it, and watch the web follow each step
# (press R in the UI to replay each causal chain). Uses your current kubectl context unless KUBECONFIG says
# otherwise; NS (default vk-demo) is created on up; down deletes the manifest's objects and keeps NS.
#   examples/01-deployment.sh up | scale [N] | down
set -euo pipefail

dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly dir
readonly ns="${NS:-vk-demo}"

echo "context: $(kubectl config current-context)  namespace: $ns"
case "${1:-}" in
  up)
    kubectl create namespace "$ns" --dry-run=client -o yaml | kubectl apply -f -
    kubectl -n "$ns" apply -f "$dir/01-deployment.yaml"
    kubectl -n "$ns" rollout status deployment/hello --timeout=120s
    ;;
  scale)
    replicas="${2:-4}"
    [[ "$replicas" =~ ^[0-9]+$ ]] || { echo "scale needs a replica count, got: $replicas" >&2; exit 2; }
    kubectl -n "$ns" scale deployment/hello --replicas="$replicas"
    kubectl -n "$ns" rollout status deployment/hello --timeout=120s
    ;;
  down)
    kubectl -n "$ns" delete -f "$dir/01-deployment.yaml" --ignore-not-found
    ;;
  *)
    echo "usage: $0 up | scale [N] | down" >&2
    exit 2
    ;;
esac
