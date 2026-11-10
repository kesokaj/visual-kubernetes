#!/usr/bin/env bash
# shellcheck disable=SC2015  # pass only echoes, so `test && pass || fail` is an if/else here
# Checks an installed visual-kubernetes on a TEST cluster: rollout, the page through
# port-forward, the ServiceAccount's read-only access, no ERROR logs, and that the
# NetworkPolicy keeps other pods out (Service and pod IP). With VK_MUST_FAIL=1 it also deletes
# the policy for a moment to show the ingress check then sees the app (its must-fail state),
# and puts it back. Never run that on a cluster people use.
#
#   KUBECONFIG=.agents/scratch/kind/kubeconfig test/kind/install-check.sh [MANIFEST]
#
# KUBECONFIG must be set: the operator's own context is never a default. MANIFEST, when
# given, is applied first. Output goes to .agents/scratch/install-check/<context>/.
set -euo pipefail

if [[ -z "${KUBECONFIG:-}" ]]; then
  echo "error: set KUBECONFIG to the test cluster's kubeconfig" >&2
  exit 2
fi
repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ns=visual-kubernetes
testns=vk-nettest
curl_image=curlimages/curl:8.11.1
ctx="$(kubectl config current-context)"
out="$repo/.agents/scratch/install-check/$ctx"
mkdir -p "$out"
failed=0
pass() { echo "PASS $*"; }
fail() { echo "FAIL $*"; failed=1; }

if [[ $# -ge 1 ]]; then
  kubectl apply -f "$1"
fi
kubectl -n "$ns" rollout status deploy/visual-kubernetes --timeout=180s && pass rollout || fail rollout

kubectl -n "$ns" port-forward svc/visual-kubernetes 18090:80 >"$out/port-forward.log" 2>&1 &
pf=$!
cleanup() {
  kill "$pf" 2>/dev/null || true
  kubectl delete namespace "$testns" --ignore-not-found --wait=false >/dev/null 2>&1 || true
}
trap cleanup EXIT
for _ in $(seq 1 20); do curl -s -o /dev/null http://127.0.0.1:18090/healthz && break; sleep 1; done
code() { curl -s -o /dev/null -w '%{http_code}' "$1" || true; }
[[ "$(code http://127.0.0.1:18090/healthz)" == 200 ]] && pass "healthz 200" || fail "healthz"
[[ "$(code http://127.0.0.1:18090/)" == 200 ]] && pass "page 200" || fail "page"
curl -sN -m 8 http://127.0.0.1:18090/api/stream >"$out/stream.txt" 2>/dev/null || true
grep -q '^event: snapshot' "$out/stream.txt" && pass "stream snapshot" || fail "stream snapshot"

sa="system:serviceaccount:$ns:visual-kubernetes"
# Positive control first: a wrong --as would answer "no" to everything below.
[[ "$(kubectl auth can-i list pods --as="$sa" 2>/dev/null || true)" == yes ]] && pass "can-i list pods: yes" || fail "can-i list pods"
for v in "create deployments" "delete pods" "list secrets" "get secrets" "patch nodes" "create pods --subresource=exec" "get nodes --subresource=proxy" "get pods --subresource=exec"; do
  # shellcheck disable=SC2086  # $v is "<verb> <resource>", split on purpose
  a="$(kubectl auth can-i $v --as="$sa" 2>/dev/null || true)"
  [[ "$a" == no ]] && pass "can-i $v: no" || fail "can-i $v: $a"
done

kubectl -n "$ns" logs deploy/visual-kubernetes >"$out/logs.txt" 2>&1 || true
n="$(grep -c '"level":"ERROR"' "$out/logs.txt" || true)"
[[ "$n" == 0 ]] && pass "no ERROR log lines" || fail "$n ERROR log lines"

# Ingress from another namespace: blocked means curl gets no answer within 5 s. A previous
# run's cleanup does not wait, so first let any terminating test namespace finish.
kubectl delete namespace "$testns" --ignore-not-found --wait=true --timeout=120s >/dev/null
kubectl create namespace "$testns" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n "$testns" run nettest --image="$curl_image" --restart=Never --command -- sleep 900 >/dev/null
kubectl -n "$testns" wait --for=condition=Ready pod/nettest --timeout=120s >/dev/null
svc_ip="$(kubectl -n "$ns" get svc visual-kubernetes -o jsonpath='{.spec.clusterIP}')"
pod_ip="$(kubectl -n "$ns" get pod -l app.kubernetes.io/name=visual-kubernetes -o jsonpath='{.items[0].status.podIP}')"
reach() { kubectl -n "$testns" exec nettest -- curl -s -m 5 -o /dev/null -w '%{http_code}' "$1" 2>/dev/null || true; }
# expect STATE: "blocked" (no answer) or "open" (200), retried for up to 30 s while policy settles.
expect() {
  local state="$1" url="$2" got=""
  for _ in $(seq 1 6); do
    got="$(reach "$url")"
    if [[ "$state" == blocked && "$got" != 200 ]] || [[ "$state" == open && "$got" == 200 ]]; then
      pass "ingress $url $state (got '${got:-none}')"
      return
    fi
    sleep 5
  done
  fail "ingress $url want $state, got '${got:-none}'"
}
expect blocked "http://$svc_ip/healthz"
expect blocked "http://$pod_ip:8080/healthz"

if [[ "${VK_MUST_FAIL:-0}" == 1 ]]; then
  kubectl -n "$ns" get networkpolicy visual-kubernetes-deny-ingress -o yaml >"$out/policy.yaml"
  kubectl -n "$ns" delete networkpolicy visual-kubernetes-deny-ingress >/dev/null
  expect open "http://$svc_ip/healthz"
  expect open "http://$pod_ip:8080/healthz"
  grep -v -e '^  resourceVersion:' -e '^  uid:' -e '^  creationTimestamp:' "$out/policy.yaml" | kubectl apply -f - >/dev/null
  expect blocked "http://$svc_ip/healthz"
  expect blocked "http://$pod_ip:8080/healthz"
fi

if [[ "$failed" != 0 ]]; then
  echo "install-check: FAIL ($ctx); logs in $out" >&2
  exit 1
fi
echo "install-check: PASS ($ctx)"
