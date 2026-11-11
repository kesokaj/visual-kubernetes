import { FILTER_GROUPS, type Edge, type Entity, type FilterGroup } from '../lib/wire'

export const WORKLOAD_KINDS = ['Deployment', 'StatefulSet', 'DaemonSet'] as const
export type WorkloadKind = (typeof WORKLOAD_KINDS)[number]

export const FOCUS_MODES = ['highlight', 'only'] as const
export type FocusMode = (typeof FOCUS_MODES)[number]

export type FilterState = {
  readonly groups: ReadonlySet<FilterGroup>
  readonly showSelf: boolean
  readonly mode: FocusMode
  readonly kinds: ReadonlySet<WorkloadKind>
  readonly namespaces: ReadonlySet<string>
}

const SELF_NAME = 'visual-kubernetes'

export const allOn = (): ReadonlySet<FilterGroup> => new Set(FILTER_GROUPS)

export function defaultFilter(): FilterState {
  return {
    groups: allOn(),
    showSelf: false,
    mode: 'highlight',
    kinds: new Set<WorkloadKind>(),
    namespaces: new Set<string>(),
  }
}

export function hasFocusFilter(f: FilterState): boolean {
  return f.kinds.size > 0 || f.namespaces.size > 0
}

export function isWorkloadKind(v: string): v is WorkloadKind {
  return (WORKLOAD_KINDS as readonly string[]).includes(v)
}

function isSelfDirect(e: Entity): boolean {
  if (e.group === 'cp' || e.kind === 'Node' || e.kind === 'Counter') return false
  if (e.namespace === SELF_NAME) return true
  if (e.labels['app.kubernetes.io/name'] === SELF_NAME) return true
  return e.namespace === 'kube-system' && (e.name === SELF_NAME || e.name.startsWith(`${SELF_NAME}-`))
}

// isSelfEntity identifies visual-kubernetes itself in both the default namespace install and the
// kube-system variant, including its pods, Service, NetworkPolicy and probe pod.
export function isSelfEntity(e: Entity, entities: ReadonlyMap<string, Entity>): boolean {
  if (isSelfDirect(e)) return true
  const app = e.appId ? entities.get(e.appId) : undefined
  if (app && isSelfDirect(app)) return true
  const owner = e.ownerId ? entities.get(e.ownerId) : undefined
  return Boolean(owner && isSelfDirect(owner))
}

export function matchesApp(e: Entity, f: FilterState): boolean {
  const kindOk = f.kinds.size === 0 || (isWorkloadKind(e.kind) && f.kinds.has(e.kind))
  const nsOk = f.namespaces.size === 0 || f.namespaces.has(e.namespace)
  return kindOk && nsOk
}

// matchesLooseSat keeps a standalone Ingress, Gateway, Service or ConfigMap in focus when its
// namespace matches (if filtering by namespace) and, when workload kinds are chosen, it links to a
// matched app.
export function matchesLooseSat(
  e: Entity,
  matchedApps: ReadonlySet<string>,
  edges: readonly Edge[],
  f: FilterState,
): boolean {
  if (f.namespaces.size > 0 && !f.namespaces.has(e.namespace)) return false
  if (f.kinds.size === 0) return e.namespace !== '' && f.namespaces.has(e.namespace)
  for (const edge of edges) {
    if (edge.from === e.id && matchedApps.has(edge.to)) return true
    if (edge.to === e.id && matchedApps.has(edge.from)) return true
  }
  return false
}

export function availableNamespaces(
  entities: ReadonlyMap<string, Entity>,
  showSelf: boolean,
  selected: ReadonlySet<string>,
): string[] {
  const out = new Set<string>(selected)
  for (const e of entities.values()) {
    if (!e.namespace || e.kind === 'Counter') continue
    if (!showSelf && isSelfEntity(e, entities)) continue
    out.add(e.namespace)
  }
  return [...out].sort((a, b) => a.localeCompare(b))
}

export function toggleInSet<T>(set: ReadonlySet<T>, item: T): ReadonlySet<T> {
  const next = new Set(set)
  if (next.has(item)) next.delete(item)
  else next.add(item)
  return next
}

export function pillLabel(f: FilterState, totalGroups: number): string {
  const shown = FILTER_GROUPS.filter((g) => f.groups.has(g)).length
  const focus = f.kinds.size + f.namespaces.size
  if (focus === 0) {
    return shown === totalGroups ? 'Showing all' : `${shown} of ${totalGroups} shown`
  }
  const verb = f.mode === 'only' ? 'Only showing' : 'Highlighting'
  return shown === totalGroups ? `${verb} (${focus})` : `${shown} of ${totalGroups} · ${verb} (${focus})`
}
