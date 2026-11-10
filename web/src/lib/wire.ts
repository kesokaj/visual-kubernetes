// Wire types of the SSE stream (plan Interfaces § SSE events; internal/model/model.go and
// internal/stream/view.go are the Go side). Every payload is checked by a guard before use:
// the stream is a trust boundary even though it is same-origin.

export const FILTER_GROUPS = [
  'controlPlane',
  'nodes',
  'workloads',
  'pods',
  'servicesConfig',
  'traffic',
  'ingress',
  'system',
] as const

export type FilterGroup = (typeof FILTER_GROUPS)[number]

export type Entity = {
  id: string
  kind: string
  group: string
  name: string
  namespace: string
  labels: Record<string, string>
  status: string
  nodeName?: string
  ownerId?: string
  appId?: string
  filterGroup: FilterGroup
  folded?: boolean
  count?: number
}

export const EDGE_KINDS = ['member', 'hosts', 'selects', 'references', 'routes', 'parent'] as const
export type EdgeKind = (typeof EDGE_KINDS)[number]

export type Edge = { id: string; from: string; to: string; kind: EdgeKind }

export type StreamEvent =
  | { type: 'snapshot'; entities: Entity[]; edges: Edge[] }
  | { type: 'upsert'; entity: Entity }
  | { type: 'delete'; id: string }
  | { type: 'edgeUpsert'; edge: Edge }
  | { type: 'edgeDelete'; id: string }

type Obj = Record<string, unknown>

function isObj(v: unknown): v is Obj {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

function isStr(v: unknown): v is string {
  return typeof v === 'string'
}

function optStr(v: unknown): boolean {
  return v === undefined || isStr(v)
}

function isLabels(v: unknown): v is Record<string, string> {
  return v === undefined || v === null || (isObj(v) && Object.values(v).every(isStr))
}

export function isFilterGroup(v: unknown): v is FilterGroup {
  return (FILTER_GROUPS as readonly unknown[]).includes(v)
}

export function isEntity(v: unknown): v is Entity {
  if (!isObj(v)) return false
  const { id, kind, group, name, namespace, status, labels, filterGroup, count, folded } = v
  return (
    [id, kind, group, name].every(isStr) &&
    optStr(namespace) &&
    optStr(status) &&
    optStr(v.nodeName) &&
    optStr(v.ownerId) &&
    optStr(v.appId) &&
    isLabels(labels) &&
    isFilterGroup(filterGroup) &&
    (count === undefined || typeof count === 'number') &&
    (folded === undefined || typeof folded === 'boolean')
  )
}

export function isEdge(v: unknown): v is Edge {
  return (
    isObj(v) &&
    [v.id, v.from, v.to].every(isStr) &&
    (EDGE_KINDS as readonly unknown[]).includes(v.kind)
  )
}

// normalize fills the fields Go may omit (null maps, empty strings) so the page never
// branches on undefined.
function normalize(e: Entity): Entity {
  return { ...e, labels: e.labels ?? {}, namespace: e.namespace ?? '', status: e.status ?? '' }
}

// keepValid drops the items that fail their guard and keeps the rest: one entity of a kind
// this page does not know yet must not blank the whole web. Each drop is logged once per
// snapshot so a model/page mismatch is visible (TestWireListsMatchModel pins the lists).
function keepValid<T>(items: unknown[], guard: (v: unknown) => v is T, what: string): T[] {
  const ok = items.filter(guard)
  if (ok.length < items.length) console.warn(`snapshot: dropped ${items.length - ok.length} invalid ${what}`)
  return ok
}

// parseEvent turns one SSE message into a typed event, or null for types this phase does not
// draw (metrics, capabilities, event) and for payloads that fail their guard.
export function parseEvent(type: string, data: string): StreamEvent | null {
  let v: unknown
  try {
    v = JSON.parse(data)
  } catch {
    return null
  }
  if (!isObj(v)) return null
  switch (type) {
    case 'snapshot': {
      const ents = keepValid(Array.isArray(v.entities) ? v.entities : [], isEntity, 'entities')
      const edges = keepValid(Array.isArray(v.edges) ? v.edges : [], isEdge, 'edges')
      return { type, entities: ents.map(normalize), edges }
    }
    case 'upsert':
      return isEntity(v.entity) ? { type, entity: normalize(v.entity) } : null
    case 'edgeUpsert':
      return isEdge(v.edge) ? { type, edge: v.edge } : null
    case 'delete':
    case 'edgeDelete':
      return isStr(v.id) ? { type, id: v.id } : null
    default:
      return null
  }
}
