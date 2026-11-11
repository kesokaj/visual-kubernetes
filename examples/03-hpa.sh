#!/usr/bin/env bash
# Example 03: the HPA walkthrough. up deploys php-apache, its Service and HPA, and an idle load
# generator; load starts the generator and the HPA scales php-apache up within a minute or
# two; calm stops it and the HPA scales back down after its 5 min stabilization window. Needs
# metrics.k8s.io. Uses your current kubectl context unless KUBECONFIG says otherwise.
#   examples/03-hpa.sh up | load | calm | down
set -euo pipefail

dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly dir
readonly ns="${NS:-vk-demo}"

echo "context: $(kubectl config current-context)  namespace: $ns"
case "${1:-}" in
  up)
    kubectl create namespace "$ns" --dry-run=client -o yaml | kubectl apply -f -
    kubectl -n "$ns" apply -f "$dir/03-hpa.yaml"
    kubectl -n "$ns" rollout status deployment/php-apache --timeout=180s
    ;;
  load)
    kubectl -n "$ns" scale deployment/load-generator --replicas=1
    kubectl -n "$ns" get hpa php-apache
    ;;
  calm)
    kubectl -n "$ns" scale deployment/load-generator --replicas=0
    kubectl -n "$ns" get hpa php-apache
    ;;
  down)
    kubectl -n "$ns" delete -f "$dir/03-hpa.yaml" --ignore-not-found
    ;;
  *)
    echo "usage: $0 up | load | calm | down" >&2
    exit 2
    ;;
esac
