#!/usr/bin/env bash
# Example 04: three-tier traffic and NetworkPolicy drops (web -> api -> cache). up deploys the
# three tiers with steady calls and a NetworkPolicy blocking direct web -> cache traffic;
# allow-direct removes the policy so web -> cache turns blue; block-direct restores it; down
# cleans up. Uses your current kubectl context unless KUBECONFIG says otherwise.
#   examples/04-traffic.sh up | allow-direct | block-direct | down
set -euo pipefail

dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly dir
readonly ns="${NS:-vk-demo}"

echo "context: $(kubectl config current-context)  namespace: $ns"
case "${1:-}" in
  up)
    kubectl create namespace "$ns" --dry-run=client -o yaml | kubectl apply -f -
    kubectl -n "$ns" apply -f "$dir/04-traffic.yaml"
    kubectl -n "$ns" rollout status deployment/cache deployment/api deployment/web --timeout=180s
    ;;
  allow-direct)
    kubectl -n "$ns" delete networkpolicy cache-only-from-api --ignore-not-found
    ;;
  block-direct)
    kubectl -n "$ns" apply -f "$dir/04-traffic.yaml"
    ;;
  down)
    kubectl -n "$ns" delete -f "$dir/04-traffic.yaml" --ignore-not-found
    ;;
  *)
    echo "usage: $0 up | allow-direct | block-direct | down" >&2
    exit 2
    ;;
esac
