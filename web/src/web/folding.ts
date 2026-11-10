import type { Entity } from '../lib/wire'

// The static control-plane pods a kubeadm-style cluster mirrors into the API (kube-apiserver-*,
// etcd-*, …) are the same parts the web already draws as cp/* entities. Drawing both shows the
// API server twice, so a static pod folds behind its cp/* entity when that entity exists
// (plan D30, page-side only: the model still sends the pod and its app).
const STATIC_CP: readonly (readonly [component: string, cpId: string])[] = [
  ['kube-apiserver', 'cp/apiserver'],
  ['etcd', 'cp/etcd'],
  ['kube-scheduler', 'cp/scheduler'],
  ['kube-controller-manager', 'cp/controller-manager'],
]

// MAX_BUBBLES is the S5 budget for bodies in the web; past it the smallest apps and loose
// satellites fold into a page-side corner counter instead of slowing the page.
export const MAX_BUBBLES = 150
export const OVER_BUDGET_ID = 'counter/page.overBudget'

// staticCpOf names the cp/* entity a bare static pod's app stands for, or null. Only a bare
// Pod in kube-system qualifies: a Deployment named etcd-operator is not a static pod.
export function staticCpOf(e: Entity): string | null {
  if (e.kind !== 'Pod' || e.namespace !== 'kube-system') return null
  const component = e.labels.component
  for (const [name, cp] of STATIC_CP) {
    if (component === name || e.name.startsWith(`${name}-`)) return cp
  }
  return null
}

// foldedStatics maps each present cp/* entity to the static-pod apps folded behind it.
export function foldedStatics(entities: ReadonlyMap<string, Entity>): Map<string, string[]> {
  const out = new Map<string, string[]>()
  for (const e of entities.values()) {
    if (!e.id.startsWith('app/')) continue
    const cp = staticCpOf(e)
    if (cp === null || !entities.has(cp)) continue
    out.set(cp, [...(out.get(cp) ?? []), e.id])
  }
  return out
}

// overBudgetCounter is the corner counter for bodies the budget left out.
export function overBudgetCounter(n: number): Entity {
  return {
    id: OVER_BUDGET_ID,
    kind: 'Counter',
    group: 'counter',
    name: 'page.overBudget',
    namespace: '',
    labels: {},
    status: `${n} more bubbles folded`,
    filterGroup: 'system',
    count: n,
  }
}
