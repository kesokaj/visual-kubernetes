// Writes the Hubble traffic stream that traffic.spec.ts replays: a 3-tier demo cluster
// (`web`, `api`, `cache` in `vk-demo`) with Hubble Relay ok and steady per-edge `flow` events
// (`FORWARDED` on `web -> api` and `api -> cache`, `DROPPED` on `web -> cache`) every second
// for ten minutes, plus an Ingress in front of `web` with the model's `ext/internet` globe routing
// to it and a FORWARDED world flow `ext/internet -> web` (plan Phase 2c). Deterministic: the same file on every run. Usage:
//   node test/e2e/fixtures/traffic.mjs <out.ndjson>
// The output belongs in .agents/scratch/ (web/playwright.config.ts does that).
import { mkdirSync, writeFileSync } from 'node:fs'
import { dirname } from 'node:path'

const out = process.argv[2]
if (!out) throw new Error('usage: traffic.mjs <out.ndjson>')

const ent = (id, kind, group, name, namespace, filterGroup, extra = {}) => ({
  id, kind, group, name, namespace, labels: {}, status: '', filterGroup, ...extra,
})
const node = (n) => ent(`core/Node/_/${n}`, 'Node', 'core', n, '', 'nodes', { status: 'Ready' })
const app = (name, pods) => ent(`app/vk-demo/Deployment/${name}`, 'Deployment', 'apps', name, 'vk-demo', 'workloads', {
  status: `${pods}/${pods} ready`, ownerId: `apps/Deployment/vk-demo/${name}`,
})
const pod = (appName, i, nodeName) =>
  ent(`core/Pod/vk-demo/${appName}-${i}`, 'Pod', 'core', `${appName}-${i}`, 'vk-demo', 'pods', {
    status: 'Running', nodeName, appId: `app/vk-demo/Deployment/${appName}`, ownerId: `apps/ReplicaSet/vk-demo/${appName}-6f8d`,
  })

const apps = [['web', 2], ['api', 2], ['cache', 1]]
const entities = [
  ent('cp/apiserver', 'APIServer', 'cp', 'apiserver', '', 'controlPlane'),
  node('node-1'),
  node('node-2'),
  ...apps.map(([n, p]) => app(n, p)),
  ...apps.flatMap(([n, p]) => Array.from({ length: p }, (_, i) => pod(n, i, i % 2 ? 'node-2' : 'node-1'))),
  // The Ingress merges into web (model D15) and the globe routes to the app, as addInternet
  // writes it; the globe's shape is the one internal/model/internet_test.go TestInternetJSON pins.
  ent('networking.k8s.io/Ingress/vk-demo/web', 'Ingress', 'networking.k8s.io', 'web', 'vk-demo', 'ingress', {
    appId: 'app/vk-demo/Deployment/web',
  }),
  ent('ext/internet', 'Internet', 'ext', 'internet', '', 'ingress', { status: '1 entrypoint' }),
]
const edge = (kind, from, to) => ({ id: `${kind}:${from}->${to}`, kind, from, to })
const edges = [
  edge('member', 'cp/apiserver', 'core/Node/_/node-1'),
  edge('member', 'cp/apiserver', 'core/Node/_/node-2'),
  ...apps.map(([n]) => edge('hosts', 'core/Node/_/node-1', `app/vk-demo/Deployment/${n}`)),
  edge('routes', 'networking.k8s.io/Ingress/vk-demo/web', 'app/vk-demo/Deployment/web'),
  edge('routes', 'ext/internet', 'app/vk-demo/Deployment/web'),
]
const capabilities = {
  discovery: { state: 'ok', detail: '14 groups' },
  events: { state: 'ok', detail: 'events.k8s.io/v1' },
  'metrics.k8s.io': { state: 'ok', detail: 'v1beta1' },
  kubelet: { state: 'ok', detail: '2 nodes' },
  'apiserver-metrics': { state: 'ok', detail: '/metrics readable' },
}

const lines = [
  { atMs: 0, event: 'snapshot', data: { entities, edges, capabilities } },
  {
    atMs: 50,
    event: 'capabilities',
    data: { capabilities: { 'hubble-relay': { state: 'ok', detail: 'relay streaming at hubble-relay.kube-system.svc:80' } } },
  },
]
const flows = [
  { src: 'app/vk-demo/Deployment/web', dst: 'app/vk-demo/Deployment/api', verdict: 'FORWARDED', bytes: 0, count: 12 },
  { src: 'app/vk-demo/Deployment/api', dst: 'app/vk-demo/Deployment/cache', verdict: 'FORWARDED', bytes: 0, count: 9 },
  { src: 'app/vk-demo/Deployment/web', dst: 'app/vk-demo/Deployment/cache', verdict: 'DROPPED', bytes: 0, count: 6 },
  { src: 'ext/internet', dst: 'app/vk-demo/Deployment/web', verdict: 'FORWARDED', bytes: 0, count: 8 },
]
for (let k = 0; k < 600; k++) {
  const atMs = 150 + k * 1000
  for (const f of flows) lines.push({ atMs, event: 'flow', data: f })
}

mkdirSync(dirname(out), { recursive: true })
writeFileSync(out, lines.map((l) => JSON.stringify(l)).join('\n') + '\n')
