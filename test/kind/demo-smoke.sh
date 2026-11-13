#!/usr/bin/env bash
# Live kind verification of installability, clean uninstallability across variants, and
# interactive --demo mode (Phase 3, D12, D20):
#   1. Exports kind kubeconfig to .agents/scratch/kind/kubeconfig if absent (never ~/.kube/config)
#   2. Builds web + visual-kubernetes:dev image and loads into kind
#   3. Server-dry-runs all 5 manifests (install, install-kube-system, install-lb, install-demo, install-iap)
#   4. Installs + rolls out + cleanly uninstalls deploy/install-kube-system.yaml (verifying kube-system
#      and coredns stay healthy and zero visual-kubernetes resources remain)
#   5. Installs deploy/install-demo.yaml, verifies live Service type switch + proxy (ClusterIP -> LoadBalancer ->
#      open -> sandboxed 200 -> revert ClusterIP -> closed 403), live Deployment scale + HPA patch, live Deployment env
#      update + 409 conflicts, live pods/exec WebSocket shell round-trip, and kube-system + default/kubernetes 403
#   6. Cleanly uninstalls deploy/install-demo.yaml (verifying zero cluster or namespaced leftovers + idempotency),
#      then restores default read-only deploy/install.yaml and verifies /api/demo/* answers 403.
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
readonly repo
readonly cluster="visual-kubernetes"
readonly ns="visual-kubernetes"
readonly smoke_ns="vk-demo-smoke"
readonly port="${VK_DEMO_PORT:-18096}"

out="$repo/.agents/scratch/kind/demo-smoke"
mkdir -p "$out"

if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
  echo "error: 127.0.0.1:$port is already bound; set VK_DEMO_PORT to a free port" >&2
  exit 1
fi

export KUBECONFIG="${KIND_KUBECONFIG:-$repo/.agents/scratch/kind/kubeconfig}"
mkdir -p "$(dirname "$KUBECONFIG")"

if kind get clusters 2>/dev/null | grep -qx "$cluster"; then
  if [[ ! -s "$KUBECONFIG" ]]; then
    kind export kubeconfig --name "$cluster" --kubeconfig "$KUBECONFIG"
  fi
else
  "$repo/test/kind/up.sh"
fi

ctx="$(kubectl config current-context)"
if [[ "$ctx" != "kind-$cluster" ]]; then
  echo "error: refusing to run demo-smoke.sh against non-kind context '$ctx' (expected kind-$cluster)" >&2
  exit 2
fi

pf_pid=""
stop_pf() {
  if [[ -n "$pf_pid" ]]; then
    kill "$pf_pid" 2>/dev/null || true
    wait "$pf_pid" 2>/dev/null || true
    pf_pid=""
  fi
}

cleanup() {
  stop_pf
  kubectl delete namespace "$smoke_ns" --ignore-not-found --wait=false >/dev/null 2>&1 || true
}
trap cleanup EXIT

start_pf() {
  local target_ns="$1"
  stop_pf
  kubectl -n "$target_ns" port-forward svc/visual-kubernetes "$port:80" >"$out/port-forward.log" 2>&1 &
  pf_pid=$!
  for _ in $(seq 1 60); do
    if ! kill -0 "$pf_pid" 2>/dev/null; then
      echo "error: port-forward exited early:" >&2
      cat "$out/port-forward.log" >&2
      exit 1
    fi
    if curl -fsS "http://127.0.0.1:$port/healthz" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.5
  done
  echo "error: http://127.0.0.1:$port/healthz did not become ready" >&2
  cat "$out/port-forward.log" >&2
  exit 1
}

assert_zero_vk_resources() {
  local label="app.kubernetes.io/name=visual-kubernetes"
  local remaining_ns="" remaining_cluster=""
  for _ in $(seq 1 60); do
    remaining_ns="$(kubectl get deploy,rs,pod,svc,sa,networkpolicy -A -l "$label" --no-headers 2>/dev/null || true)"
    remaining_cluster="$(kubectl get ns,clusterrole,clusterrolebinding -l "$label" --no-headers 2>/dev/null || true)"
    if [[ -z "$remaining_ns" && -z "$remaining_cluster" ]]; then
      echo "PASS clean uninstall left zero visual-kubernetes resources"
      return 0
    fi
    sleep 0.5
  done
  echo "FAIL: leftover visual-kubernetes resources after uninstall:" >&2
  printf '%s\n%s\n' "$remaining_ns" "$remaining_cluster" >&2
  exit 1
}

if [[ "${VK_DEMO_SKIP_BUILD:-0}" != 1 ]]; then
  pnpm -C "$repo/web" build
  docker build -t visual-kubernetes:dev "$repo"
  kind load docker-image visual-kubernetes:dev --name "$cluster"
fi

# 1. Start from a clean slate so cold-cluster install & uninstall checks start with zero resources.
kubectl delete --ignore-not-found --wait=true --timeout=120s \
  -f "$repo/deploy/install.yaml" \
  -f "$repo/deploy/install-kube-system.yaml" >/dev/null
assert_zero_vk_resources

# 2. Cold-cluster install + rollout + clean uninstall of deploy/install-kube-system.yaml.
kubectl apply -f "$repo/deploy/install-kube-system.yaml" >/dev/null
kubectl -n kube-system set image deploy/visual-kubernetes visual-kubernetes=visual-kubernetes:dev >/dev/null
kubectl -n kube-system rollout status deploy/visual-kubernetes --timeout=180s >/dev/null
echo "PASS install-kube-system.yaml rollout"

kubectl delete -f "$repo/deploy/install-kube-system.yaml" --wait=true --timeout=120s >/dev/null
kubectl delete --ignore-not-found -f "$repo/deploy/install-kube-system.yaml" >/dev/null
if [[ "$(kubectl get ns kube-system -o jsonpath='{.status.phase}')" != "Active" ]]; then
  echo "FAIL: kube-system namespace not Active after uninstall" >&2
  exit 1
fi
kubectl -n kube-system rollout status deploy/coredns --timeout=60s >/dev/null
assert_zero_vk_resources
echo "PASS install-kube-system.yaml clean uninstall kept kube-system & coredns healthy"

# 3. Cold-cluster install of deploy/install-demo.yaml (creates Namespace/visual-kubernetes in 1 command),
#    then server dry-run all 5 manifests against the live Kubernetes API server.
kubectl apply -f "$repo/deploy/install-demo.yaml" >/dev/null
for manifest in install.yaml install-kube-system.yaml install-lb.yaml install-demo.yaml install-iap.yaml; do
  kubectl apply --dry-run=server -f "$repo/deploy/$manifest" >/dev/null
  echo "PASS server dry-run deploy/$manifest"
done
kubectl -n "$ns" set image deploy/visual-kubernetes visual-kubernetes=visual-kubernetes:dev >/dev/null
kubectl -n "$ns" rollout status deploy/visual-kubernetes --timeout=180s >/dev/null
echo "PASS install-demo.yaml rollout"

kubectl delete namespace "$smoke_ns" --ignore-not-found --wait=true --timeout=120s >/dev/null
kubectl create namespace "$smoke_ns" >/dev/null
kubectl -n "$smoke_ns" apply -f "$repo/examples/01-deployment.yaml" >/dev/null
kubectl -n "$smoke_ns" autoscale deploy/hello --min=1 --max=3 >/dev/null
kubectl -n "$smoke_ns" rollout status deploy/hello --timeout=180s >/dev/null

start_pf "$ns"

# Wait for informer model to observe vk-demo-smoke/hello Service, Deployment, and HPA.
smoke_pod="$(kubectl -n "$smoke_ns" get pod -l app.kubernetes.io/name=hello -o jsonpath='{.items[0].metadata.name}')"
svc_ready=0
hpa_ready=0
for _ in $(seq 1 40); do
  if [[ "$svc_ready" == 0 ]]; then
    code="$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$port/api/demo/services/type?id=core%2FService%2F${smoke_ns}%2Fhello")"
    [[ "$code" == 200 ]] && svc_ready=1
  fi
  if [[ "$hpa_ready" == 0 ]]; then
    code="$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$port/api/demo/autoscalers?id=autoscaling%2FHorizontalPodAutoscaler%2F${smoke_ns}%2Fhello")"
    [[ "$code" == 200 ]] && hpa_ready=1
  fi
  if [[ "$svc_ready" == 1 && "$hpa_ready" == 1 ]]; then
    break
  fi
  sleep 0.5
done
if [[ "$svc_ready" != 1 || "$hpa_ready" != 1 ]]; then
  echo "FAIL: timed out waiting for demo informer model (svc_ready=$svc_ready hpa_ready=$hpa_ready)" >&2
  exit 1
fi

# 4. Run end-to-end live HTTP + WebSocket assertions via Node >= 22 built-in fetch & WebSocket.
SMOKE_BASE="http://127.0.0.1:$port" SMOKE_NS="$smoke_ns" SMOKE_POD="$smoke_pod" node - <<'EOF'
const assert = require('node:assert/strict');
const http = require('node:http');

const base = process.env.SMOKE_BASE;
const baseUrl = new URL(base);
const ns = process.env.SMOKE_NS;
const pod = process.env.SMOKE_POD;

function getWithHost(path, hostHeader) {
  return new Promise((resolve, reject) => {
    const req = http.get(
      { hostname: baseUrl.hostname, port: baseUrl.port, path, headers: { Host: hostHeader } },
      (res) => {
        res.resume();
        res.on('end', () => resolve(res.statusCode));
      },
    );
    req.on('error', reject);
  });
}

async function main() {
  // Non-loopback Host without --iap-audience must be refused with 403 (node:http sends raw Host).
  const badHostStatus = await getWithHost('/api/demo/ports', '10.244.0.5:8080');
  assert.equal(badHostStatus, 403, 'non-loopback Host must return 403 without IAP');

  // Protected namespace and default/kubernetes Service refused across demo endpoints.
  const sysPort = await fetch(`${base}/api/demo/ports`, {
    method: 'POST',
    headers: { Origin: base, 'Content-Type': 'application/json' },
    body: JSON.stringify({ namespace: 'kube-system', service: 'kube-dns', port: 53, open: true }),
  });
  assert.equal(sysPort.status, 403, 'kube-system port toggle must return 403');

  const apiSvcGet = await fetch(`${base}/api/demo/services/type?id=core%2FService%2Fdefault%2Fkubernetes`);
  assert.equal(apiSvcGet.status, 403, 'default/kubernetes Service type GET must return 403');

  const apiSvcPatch = await fetch(`${base}/api/demo/services/type`, {
    method: 'PATCH',
    headers: { Origin: base, 'Content-Type': 'application/json' },
    body: JSON.stringify({ id: 'core/Service/default/kubernetes', resourceVersion: '1', type: 'LoadBalancer' }),
  });
  assert.equal(apiSvcPatch.status, 403, 'default/kubernetes Service type PATCH must return 403');

  const sysScale = await fetch(`${base}/api/demo/workloads/scale?id=apps/Deployment/kube-system/coredns`);
  assert.equal(sysScale.status, 403, 'kube-system scale GET must return 403');

  const sysHpa = await fetch(`${base}/api/demo/autoscalers?id=autoscaling/HorizontalPodAutoscaler/kube-system/sys`);
  assert.equal(sysHpa.status, 403, 'kube-system autoscaler GET must return 403');

  const sysEnv = await fetch(`${base}/api/demo/workloads/env?id=apps/Deployment/kube-system/coredns`);
  assert.equal(sysEnv.status, 403, 'kube-system env GET must return 403');

  const sysExec = await fetch(`${base}/api/demo/exec?id=core/Pod/kube-system/coredns-0&shell=/bin/sh`, {
    headers: { Origin: base },
  });
  assert.equal(sysExec.status, 403, 'kube-system exec must return 403');

  // Service type switch (ClusterIP -> LoadBalancer) -> proxy 200 -> revert ClusterIP -> 403.
  const closedTry = await fetch(`${base}/api/demo/proxy/t/00000000000000000000000000000000/`);
  assert.equal(closedTry.status, 403, 'unopened proxy token must return 403');

  const svcId = `core/Service/${ns}/hello`;
  const svcGet = await fetch(`${base}/api/demo/services/type?id=${encodeURIComponent(svcId)}`);
  assert.equal(svcGet.status, 200, 'Service type GET must return 200');
  const svcInitial = await svcGet.json();
  assert.equal(svcInitial.type, 'ClusterIP', 'Service hello must start as ClusterIP');

  const svcToLB = await fetch(`${base}/api/demo/services/type`, {
    method: 'PATCH',
    headers: { Origin: base, 'Content-Type': 'application/json' },
    body: JSON.stringify({ id: svcId, resourceVersion: svcInitial.resourceVersion, type: 'LoadBalancer' }),
  });
  assert.equal(svcToLB.status, 200, 'switching Service hello ClusterIP -> LoadBalancer must return 200');
  const svcLB = await svcToLB.json();
  assert.equal(svcLB.type, 'LoadBalancer');

  let exposed = false;
  for (let i = 0; i < 20; i++) {
    const listRes = await fetch(`${base}/api/demo/ports`);
    assert.equal(listRes.status, 200);
    const listJson = await listRes.json();
    if (listJson.ports.some((p) => p.namespace === ns && p.service === 'hello' && p.port === 80)) {
      exposed = true;
      break;
    }
    await new Promise((r) => setTimeout(r, 250));
  }
  assert.ok(exposed, 'switched LoadBalancer Service hello must appear in /api/demo/ports');

  const openRes = await fetch(`${base}/api/demo/ports`, {
    method: 'POST',
    headers: { Origin: base, 'Content-Type': 'application/json' },
    body: JSON.stringify({ namespace: ns, service: 'hello', port: 80, open: true }),
  });
  assert.equal(openRes.status, 200, 'opening exposed LoadBalancer Service must return 200');
  const openData = await openRes.json();
  const entry = openData.ports.find((p) => p.namespace === ns && p.service === 'hello' && p.port === 80);
  assert.ok(entry && entry.open && entry.pathPrefix, 'opened port must return pathPrefix');

  const proxied = await fetch(`${base}${entry.pathPrefix}/`);
  assert.equal(proxied.status, 200, 'proxied Service GET / must return 200');
  const csp = proxied.headers.get('content-security-policy') || '';
  assert.ok(csp.includes('sandbox allow-scripts allow-forms'), `missing CSP sandbox: ${csp}`);
  const html = await proxied.text();
  assert.ok(html.toLowerCase().includes('nginx'), 'expected nginx welcome HTML through proxy');

  const svcRevert = await fetch(`${base}/api/demo/services/type`, {
    method: 'PATCH',
    headers: { Origin: base, 'Content-Type': 'application/json' },
    body: JSON.stringify({ id: svcId, resourceVersion: svcLB.resourceVersion, type: 'ClusterIP' }),
  });
  assert.equal(svcRevert.status, 200, 'reverting Service hello LoadBalancer -> ClusterIP must return 200');
  const reverted = await fetch(`${base}${entry.pathPrefix}/`);
  assert.equal(reverted.status, 403, 'reverting Service to ClusterIP must immediately revoke proxy token with 403');
  console.log('PASS live Service type switch (ClusterIP <-> LoadBalancer) + proxy open/browse/auto-revoke');

  // Pod exec WebSocket round-trip against real kubelet/containerd PTY (before scale/env rollouts replace smoke_pod).
  const wsUrl = `${base.replace(/^http/, 'ws')}/api/demo/exec?id=${encodeURIComponent(`core/Pod/${ns}/${pod}`)}&shell=/bin/sh`;
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('timed out waiting for pods/exec output')), 15000);
    const ws = new WebSocket(wsUrl, { headers: { Origin: base } });
    let combined = '';
    ws.addEventListener('open', () => {
      ws.send(JSON.stringify({ type: 'resize', cols: 80, rows: 24 }));
      ws.send(JSON.stringify({ type: 'stdin', data: 'echo kind-live-exec-ok\nexit\n' }));
    });
    ws.addEventListener('message', (ev) => {
      const frame = JSON.parse(String(ev.data));
      if (frame.type === 'stdout') combined += frame.data;
      if (combined.includes('kind-live-exec-ok') && frame.type === 'exit') {
        clearTimeout(timer);
        ws.close();
        resolve();
      }
    });
    ws.addEventListener('error', (err) => {
      clearTimeout(timer);
      reject(err);
    });
    ws.addEventListener('close', () => {
      if (combined.includes('kind-live-exec-ok')) {
        clearTimeout(timer);
        resolve();
      }
    });
  });
  console.log('PASS live pods/exec WebSocket round-trip');

  // Workload scale editor: GET -> PATCH 2 -> 3 -> 409 stale conflict.
  const workloadId = `apps/Deployment/${ns}/hello`;
  const scaleGet = await fetch(`${base}/api/demo/workloads/scale?id=${encodeURIComponent(workloadId)}`);
  assert.equal(scaleGet.status, 200);
  const scaleInitial = await scaleGet.json();
  assert.equal(scaleInitial.replicas, 2);
  assert.equal(scaleInitial.managedByHPA, 'hello');

  const scalePatch = await fetch(`${base}/api/demo/workloads/scale`, {
    method: 'PATCH',
    headers: { Origin: base, 'Content-Type': 'application/json' },
    body: JSON.stringify({ id: workloadId, resourceVersion: scaleInitial.resourceVersion, replicas: 3 }),
  });
  assert.equal(scalePatch.status, 200, 'live Deployment scale PATCH must return 200');
  const scaleUpdated = await scalePatch.json();
  assert.equal(scaleUpdated.replicas, 3);

  const staleScalePatch = await fetch(`${base}/api/demo/workloads/scale`, {
    method: 'PATCH',
    headers: { Origin: base, 'Content-Type': 'application/json' },
    body: JSON.stringify({ id: workloadId, resourceVersion: scaleInitial.resourceVersion, replicas: 1 }),
  });
  assert.equal(staleScalePatch.status, 409, 'stale resourceVersion scale PATCH must return 409');
  console.log('PASS live Deployment scale update and 409 conflict');

  // HPA autoscaler editor: GET -> PATCH min=2, max=4 -> 409 stale conflict.
  const hpaId = `autoscaling/HorizontalPodAutoscaler/${ns}/hello`;
  const hpaGet = await fetch(`${base}/api/demo/autoscalers?id=${encodeURIComponent(hpaId)}`);
  assert.equal(hpaGet.status, 200);
  const hpaInitial = await hpaGet.json();
  assert.equal(hpaInitial.hpa.minReplicas, 1);
  assert.equal(hpaInitial.hpa.maxReplicas, 3);

  const hpaPatch = await fetch(`${base}/api/demo/autoscalers`, {
    method: 'PATCH',
    headers: { Origin: base, 'Content-Type': 'application/json' },
    body: JSON.stringify({
      id: hpaId,
      resourceVersion: hpaInitial.resourceVersion,
      hpa: { minReplicas: 2, maxReplicas: 4 },
    }),
  });
  assert.equal(hpaPatch.status, 200, 'live HPA PATCH must return 200');

  const staleHpaPatch = await fetch(`${base}/api/demo/autoscalers`, {
    method: 'PATCH',
    headers: { Origin: base, 'Content-Type': 'application/json' },
    body: JSON.stringify({
      id: hpaId,
      resourceVersion: hpaInitial.resourceVersion,
      hpa: { minReplicas: 1, maxReplicas: 5 },
    }),
  });
  assert.equal(staleHpaPatch.status, 409, 'stale resourceVersion HPA PATCH must return 409');
  console.log('PASS live HPA update and 409 conflict');

  // Workload env editor: GET -> PATCH (retrying transient 409 from concurrent controller status update) -> 409 stale conflict.
  let envBase;
  let envUpdated;
  for (let attempt = 0; attempt < 4; attempt++) {
    const envGet = await fetch(`${base}/api/demo/workloads/env?id=${encodeURIComponent(workloadId)}`);
    assert.equal(envGet.status, 200);
    envBase = await envGet.json();
    const envPatch = await fetch(`${base}/api/demo/workloads/env`, {
      method: 'PATCH',
      headers: { Origin: base, 'Content-Type': 'application/json' },
      body: JSON.stringify({
        id: workloadId,
        resourceVersion: envBase.resourceVersion,
        container: 'web',
        env: [{ name: 'DEMO_STAGE', value: 'kind-live' }],
      }),
    });
    if (envPatch.status === 409) {
      await new Promise((r) => setTimeout(r, 200));
      continue;
    }
    assert.equal(envPatch.status, 200, 'live Deployment env PATCH must return 200');
    envUpdated = await envPatch.json();
    break;
  }
  assert.ok(envUpdated, 'live Deployment env PATCH succeeded after concurrent controller status updates');
  assert.notEqual(envUpdated.resourceVersion, envBase.resourceVersion);

  const stalePatch = await fetch(`${base}/api/demo/workloads/env`, {
    method: 'PATCH',
    headers: { Origin: base, 'Content-Type': 'application/json' },
    body: JSON.stringify({
      id: workloadId,
      resourceVersion: envBase.resourceVersion,
      container: 'web',
      env: [{ name: 'DEMO_STAGE', value: 'stale' }],
    }),
  });
  assert.equal(stalePatch.status, 409, 'stale resourceVersion PATCH must return 409');
  console.log('PASS live Deployment env update and 409 conflict');
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
EOF

kubectl -n "$smoke_ns" rollout status deploy/hello --timeout=180s >/dev/null
got_svc_type="$(kubectl -n "$smoke_ns" get svc hello -o jsonpath='{.spec.type}')"
if [[ "$got_svc_type" != "ClusterIP" ]]; then
  echo "FAIL: expected Service/hello type ClusterIP after revert, got '$got_svc_type'" >&2
  exit 1
fi
got_replicas="$(kubectl -n "$smoke_ns" get deploy hello -o jsonpath='{.spec.replicas}')"
if [[ "$got_replicas" != "3" ]]; then
  echo "FAIL: expected Deployment/hello replicas=3, got '$got_replicas'" >&2
  exit 1
fi
got_hpa="$(kubectl -n "$smoke_ns" get hpa hello -o jsonpath='{.spec.minReplicas},{.spec.maxReplicas}')"
if [[ "$got_hpa" != "2,4" ]]; then
  echo "FAIL: expected HPA/hello 2,4, got '$got_hpa'" >&2
  exit 1
fi
got_env="$(kubectl -n "$smoke_ns" get deploy hello -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="DEMO_STAGE")].value}')"
if [[ "$got_env" != "kind-live" ]]; then
  echo "FAIL: expected DEMO_STAGE=kind-live on Deployment/hello, got '$got_env'" >&2
  exit 1
fi
echo "PASS live Deployment/Service/HPA checks confirmed on cluster objects"

stop_pf
kubectl delete namespace "$smoke_ns" --ignore-not-found --wait=true --timeout=120s >/dev/null

# 5. Clean uninstall of deploy/install-demo.yaml + idempotent re-delete.
kubectl delete -f "$repo/deploy/install-demo.yaml" --wait=true --timeout=120s >/dev/null
assert_zero_vk_resources
kubectl delete --ignore-not-found -f "$repo/deploy/install-demo.yaml" >/dev/null
echo "PASS install-demo.yaml clean & idempotent uninstall"

# 6. Restore default read-only deploy/install.yaml and verify /api/demo/* answers 403.
kubectl apply -f "$repo/deploy/install.yaml" >/dev/null
kubectl -n "$ns" set image deploy/visual-kubernetes visual-kubernetes=visual-kubernetes:dev >/dev/null
kubectl -n "$ns" rollout status deploy/visual-kubernetes --timeout=180s >/dev/null
start_pf "$ns"
demo_off_code="$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$port/api/demo/ports")"
if [[ "$demo_off_code" != "403" ]]; then
  echo "FAIL: default deploy/install.yaml /api/demo/ports returned $demo_off_code, want 403" >&2
  exit 1
fi
stop_pf
echo "PASS restored read-only deploy/install.yaml (/api/demo/ports -> 403)"
echo "demo-smoke: PASS ($ctx)"
