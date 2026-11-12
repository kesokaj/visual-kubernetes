// Writes the telemetry stream the panel and sizing specs replay: a small cluster plus
// `metrics` every second and Kubernetes `event` records every five seconds for ten minutes,
// so a page loaded at any point of the run sees samples and Events (the replay plays once
// and snapshots carry neither). Deterministic: the same file on every run. Usage:
//   node test/e2e/fixtures/telemetry.mjs <out.ndjson>
// The output belongs in .agents/scratch/ (web/playwright.config.ts does that).
import { mkdirSync, writeFileSync } from 'node:fs'
import { dirname } from 'node:path'

const out = process.argv[2]
if (!out) throw new Error('usage: telemetry.mjs <out.ndjson>')

const ent = (id, kind, group, name, namespace, filterGroup, extra = {}) => ({
  id, kind, group, name, namespace, labels: {}, status: '', filterGroup, ...extra,
})
const node = (n) => ent(`core/Node/_/${n}`, 'Node', 'core', n, '', 'nodes', { status: 'Ready' })
const app = (ns, name, pods, extra = {}) => ent(`app/${ns}/Deployment/${name}`, 'Deployment', 'apps', name, ns, 'workloads', {
  status: `${pods}/${pods} ready`, ownerId: `apps/Deployment/${ns}/${name}`, ...extra,
})
const pod = (ns, appName, i, nodeName, appId = `app/${ns}/Deployment/${appName}`) =>
  ent(`core/Pod/${ns}/${appName}-x${i}`, 'Pod', 'core', `${appName}-x${i}`, ns, 'pods', {
    status: 'Running', nodeName, appId, ownerId: `apps/ReplicaSet/${ns}/${appName}-7d4b9c`,
  })
// A bare static pod is its own app (D23); kube-apiserver and etcd have cp/* parts and fold
// behind them (D30), kube-scheduler has none here and stays drawn.
const staticApp = (comp) => ent(`app/kube-system/Pod/${comp}-node-1`, 'Pod', 'core', `${comp}-node-1`, 'kube-system', 'system', {
  status: 'Running', labels: { component: comp, tier: 'control-plane' },
})
const staticPod = (comp) => ent(`core/Pod/kube-system/${comp}-node-1`, 'Pod', 'core', `${comp}-node-1`, 'kube-system', 'pods', {
  status: 'Running', nodeName: 'node-1', appId: `app/kube-system/Pod/${comp}-node-1`, labels: { component: comp },
})

const apps = [['web', 3], ['api', 2], ['cart', 1], ['batch', 1]]
const entities = [
  ent('cp/apiserver', 'APIServer', 'cp', 'apiserver', '', 'controlPlane'),
  ent('cp/etcd', 'Etcd', 'cp', 'etcd', '', 'controlPlane', { status: 'Healthy', labels: { 'visual-kubernetes/evidence': 'lease' } }),
  node('node-1'),
  node('node-2'),
  ...apps.map(([n, p]) => app('shop', n, p)),
  ...apps.flatMap(([n, p]) => Array.from({ length: p }, (_, i) => pod('shop', n, i, i % 2 ? 'node-2' : 'node-1'))),
  ...['kube-apiserver', 'etcd', 'kube-scheduler'].map(staticApp),
  ...['kube-apiserver', 'etcd', 'kube-scheduler'].map(staticPod),
]
const edge = (kind, from, to) => ({ id: `${kind}:${from}->${to}`, kind, from, to })
const edges = [
  edge('member', 'cp/apiserver', 'core/Node/_/node-1'),
  edge('member', 'cp/apiserver', 'core/Node/_/node-2'),
  edge('member', 'cp/apiserver', 'cp/etcd'),
  ...apps.map(([n]) => edge('hosts', 'core/Node/_/node-1', `app/shop/Deployment/${n}`)),
]
const capabilities = {
  discovery: { state: 'ok', detail: '14 groups' },
  events: { state: 'ok', detail: 'events.k8s.io/v1' },
  'metrics.k8s.io': { state: 'ok', detail: 'v1beta1' },
  kubelet: { state: 'ok', detail: '2 nodes' },
  'apiserver-metrics': { state: 'ok', detail: '/metrics readable' },
  'hubble-relay': { state: 'missing', detail: 'no hubble-relay Service in kube-system' },
}

const lines = [{ atMs: 0, event: 'snapshot', data: { entities, edges, capabilities } }]
// Network rates per app in bytes/s: web is the busiest, so it gets halo 1 and the biggest icon.
// CPU and memory lead with other apps (cart burns CPU, batch holds a big cache) so the size
// switch (PRD C3, size.spec.ts) shows a different top app per metric; pods lead with web (3).
const rate = { web: 12e6, api: 5e6, cart: 1.5e6, batch: 2e5 }
const cpuMilli = { web: 120, api: 90, cart: 400, batch: 30 }
const memMiB = { web: 160, api: 128, cart: 96, batch: 640 }
for (let k = 0; k < 600; k++) {
  const wave = 1 + 0.3 * Math.sin(k / 3)
  const samples = apps.flatMap(([n, p]) => {
    const rx = Math.round(rate[n] * wave * 0.6)
    const tx = Math.round(rate[n] * wave * 0.4)
    const cpu = Math.round(cpuMilli[n] * wave)
    const mem = memMiB[n] * 1024 * 1024
    const appSample = { id: `app/shop/Deployment/${n}`, rxBps: rx, txBps: tx, cpuMilli: cpu, memBytes: mem }
    const podSamples = Array.from({ length: p }, (_, i) => ({
      id: `core/Pod/shop/${n}-x${i}`, rxBps: Math.round(rx / p), txBps: Math.round(tx / p),
      cpuMilli: Math.round(cpu / p), memBytes: Math.round(mem / p),
    }))
    return [appSample, ...podSamples]
  })
  lines.push({ atMs: 200 + k * 1000, event: 'metrics', data: { sizeBy: 'network', samples } })
}
// The same Events repeat with the same times: the page lists each once, and a page loaded
// late still receives them within five seconds.
const events = [
  { id: 'apps/Deployment/shop/web', type: 'Normal', reason: 'ScalingReplicaSet', message: 'Scaled up replica set web-7d4b9c to 3', at: '2026-11-10T10:00:00Z' },
  { id: 'core/Pod/shop/web-x2', type: 'Normal', reason: 'Scheduled', message: 'Successfully assigned shop/web-x2 to node-1', at: '2026-11-10T10:00:01Z' },
  { id: 'core/Pod/shop/api-x1', type: 'Warning', reason: 'BackOff', message: 'Back-off restarting failed container', at: '2026-11-10T10:00:02Z' },
  { id: 'core/Node/_/node-2', type: 'Normal', reason: 'NodeReady', message: 'Node node-2 status is now: NodeReady', at: '2026-11-10T10:00:03Z' },
]
for (let k = 0; k < 120; k++) {
  for (const e of events) lines.push({ atMs: 300 + k * 5000, event: 'event', data: { k8sEvent: e } })
}
lines.sort((a, b) => a.atMs - b.atMs)
mkdirSync(dirname(out), { recursive: true })
writeFileSync(out, lines.map((l) => JSON.stringify(l)).join('\n') + '\n')
