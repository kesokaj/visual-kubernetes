#!/usr/bin/env bash
# Example 02: scale a Deployment of one-CPU pods past what the nodes hold. The extra pods stay
# Pending (FailedScheduling); with node autoscaling new workers join and take them. Uses your
# current kubectl context unless KUBECONFIG says otherwise; NS defaults to vk-demo.
#   examples/02-nodes.sh up | more [N] | down
set -euo pipefail

dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly dir
readonly ns="${NS:-vk-demo}"

echo "context: $(kubectl config current-context)  namespace: $ns"
case "${1:-}" in
  up)
    kubectl create namespace "$ns" --dry-run=client -o yaml | kubectl apply -f -
    kubectl -n "$ns" apply -f "$dir/02-nodes.yaml"
    ;;
  more)
    replicas="${2:-8}"
    [[ "$replicas" =~ ^[0-9]+$ ]] || { echo "more needs a replica count, got: $replicas" >&2; exit 2; }
    kubectl -n "$ns" scale deployment/big --replicas="$replicas"
    kubectl -n "$ns" get pods -l app.kubernetes.io/name=big -o wide
    ;;
  down)
    kubectl -n "$ns" delete -f "$dir/02-nodes.yaml" --ignore-not-found
    ;;
  *)
    echo "usage: $0 up | more [N] | down" >&2
    exit 2
    ;;
esac
