// Writes the deterministic interactive-demo stream (.agents/scratch/e2e/demo.ndjson) replayed on
// 127.0.0.1:18090 with `--demo` (D10). Includes:
//   - shop/web Deployment + 2 Running Pods + exposed LoadBalancer Service shop/web + ext/internet
//   - shop/internal Deployment + 1 Running Pod + internal ClusterIP Service (not exposed)
//   - kube-system/coredns Deployment + Running Pod + NodePort Service (protected namespace -> 403)
// Usage: node test/e2e/fixtures/demo.mjs <out.ndjson>
import { mkdirSync, writeFileSync } from 'node:fs'
import { dirname } from 'node:path'

const out = process.argv[2]
if (!out) throw new Error('usage: demo.mjs <out.ndjson>')

const ent = (id, kind, group, name, namespace, filterGroup, extra = {}) => ({
  id,
  kind,
  group,
  name,
  namespace,
  labels: {},
  status: '',
  filterGroup,
  ...extra,
})

const entities = [
  ent('cp/apiserver', 'APIServer', 'cp', 'apiserver', '', 'controlPlane'),
  ent('core/Node/_/node-1', 'Node', 'core', 'node-1', '', 'nodes', { status: 'Ready' }),
  ent('app/shop/Deployment/web', 'Deployment', 'apps', 'web', 'shop', 'workloads', {
    status: '2/2 ready',
    ownerId: 'apps/Deployment/shop/web',
  }),
  ent('core/Pod/shop/web-0', 'Pod', 'core', 'web-0', 'shop', 'pods', {
    status: 'Running',
    nodeName: 'node-1',
    appId: 'app/shop/Deployment/web',
    ownerId: 'apps/ReplicaSet/shop/web-7d4b',
  }),
  ent('core/Pod/shop/web-1', 'Pod', 'core', 'web-1', 'shop', 'pods', {
    status: 'Running',
    nodeName: 'node-1',
    appId: 'app/shop/Deployment/web',
    ownerId: 'apps/ReplicaSet/shop/web-7d4b',
  }),
  ent('core/Service/shop/web', 'Service', 'core', 'web', 'shop', 'servicesConfig', {
    status: 'LoadBalancer',
    appId: 'app/shop/Deployment/web',
  }),
  ent('ext/internet', 'Internet', 'ext', 'internet', '', 'ingress', { status: '1 entrypoint' }),
  ent('app/shop/Deployment/worker', 'Deployment', 'apps', 'worker', 'shop', 'workloads', {
    status: '1/1 ready',
    ownerId: 'apps/Deployment/shop/worker',
  }),
  ent('core/Pod/shop/worker-0', 'Pod', 'core', 'worker-0', 'shop', 'pods', {
    status: 'Running',
    nodeName: 'node-1',
    appId: 'app/shop/Deployment/worker',
    ownerId: 'apps/ReplicaSet/shop/worker-5c9a',
  }),
  ent('core/Service/shop/worker', 'Service', 'core', 'worker', 'shop', 'servicesConfig', {
    status: 'ClusterIP',
    appId: 'app/shop/Deployment/worker',
  }),
  ent('app/shop/StatefulSet/db', 'StatefulSet', 'apps', 'db', 'shop', 'workloads', {
    status: '1/1 ready',
    ownerId: 'apps/StatefulSet/shop/db',
  }),
  ent('app/shop/DaemonSet/agent', 'DaemonSet', 'apps', 'agent', 'shop', 'workloads', {
    status: '1/1 ready',
    ownerId: 'apps/DaemonSet/shop/agent',
  }),
  ent('app/kube-system/Deployment/coredns', 'Deployment', 'apps', 'coredns', 'kube-system', 'system', {
    status: '1/1 ready',
    ownerId: 'apps/Deployment/kube-system/coredns',
  }),
  ent('core/Pod/kube-system/coredns-0', 'Pod', 'core', 'coredns-0', 'kube-system', 'system', {
    status: 'Running',
    nodeName: 'node-1',
    appId: 'app/kube-system/Deployment/coredns',
    ownerId: 'apps/ReplicaSet/kube-system/coredns-6f8d',
  }),
  ent('core/Service/kube-system/kube-dns', 'Service', 'core', 'kube-dns', 'kube-system', 'system', {
    status: 'NodePort',
    appId: 'app/kube-system/Deployment/coredns',
  }),
]

const edge = (kind, from, to) => ({ id: `${kind}:${from}->${to}`, kind, from, to })
const edges = [
  edge('member', 'cp/apiserver', 'core/Node/_/node-1'),
  edge('hosts', 'core/Node/_/node-1', 'app/shop/Deployment/web'),
  edge('hosts', 'core/Node/_/node-1', 'app/shop/Deployment/worker'),
  edge('hosts', 'core/Node/_/node-1', 'app/shop/StatefulSet/db'),
  edge('hosts', 'core/Node/_/node-1', 'app/shop/DaemonSet/agent'),
  edge('hosts', 'core/Node/_/node-1', 'app/kube-system/Deployment/coredns'),
  edge('routes', 'ext/internet', 'app/shop/Deployment/web'),
]

const capabilities = {
  discovery: { state: 'ok', detail: '14 groups' },
  events: { state: 'ok', detail: 'events.k8s.io/v1' },
  'metrics.k8s.io': { state: 'ok', detail: 'v1beta1' },
  kubelet: { state: 'ok', detail: '1 node' },
  'apiserver-metrics': { state: 'ok', detail: '/metrics readable' },
}

const lines = [{ atMs: 0, event: 'snapshot', data: { entities, edges, capabilities } }]

mkdirSync(dirname(out), { recursive: true })
writeFileSync(out, lines.map((l) => JSON.stringify(l)).join('\n') + '\n')
