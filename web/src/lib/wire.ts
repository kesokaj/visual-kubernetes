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

// Capability is one source's state (probe states: ok, missing, denied, unreachable, error).
export type Capability = { state: string; detail: string }
export type Capabilities = Record<string, Capability>

// SizeBy is the measure app bubbles are sized by this cycle (PRD S2, plan D24).
export const SIZE_BY = ['network', 'cpu', 'pods'] as const
export type SizeBy = (typeof SIZE_BY)[number]

export type Sample = { id: string; rxBps?: number; txBps?: number; cpuMilli?: number; memBytes?: number }

// K8sEvent is the page-side shape of an `event` SSE message (plan D32): `id` is the entity the
// Event is about (its regarding object, as a D13 id).
export type K8sEvent = { id: string; type: string; reason: string; message: string; at: string }

// Chain is one causal chain (plan D17, internal/replay): the steps a change took through the
// cluster, oldest first. Derived steps (authentication, authorization, admission, persisted)
// are inferred from the API server's request path, never observed. A chain is resent whole
// each time it grows, so the newest copy of an id replaces the older one.
export type Step = { at: string; actor: string; verb: string; object: string; derived: boolean }
export type Chain = { id: string; steps: Step[] }

export type StreamEvent =
  | { type: 'snapshot'; entities: Entity[]; edges: Edge[]; capabilities: Capabilities }
  | { type: 'upsert'; entity: Entity }
  | { type: 'delete'; id: string }
  | { type: 'edgeUpsert'; edge: Edge }
  | { type: 'edgeDelete'; id: string }
  | { type: 'metrics'; sizeBy: SizeBy; samples: Sample[] }
  | { type: 'capabilities'; capabilities: Capabilities }
  | { type: 'event'; event: K8sEvent }
  | { type: 'chain'; chain: Chain }

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

function optNum(v: unknown): boolean {
  return v === undefined || (typeof v === 'number' && Number.isFinite(v))
}

export function isSample(v: unknown): v is Sample {
  return (
    isObj(v) && isStr(v.id) && optNum(v.rxBps) && optNum(v.txBps) && optNum(v.cpuMilli) && optNum(v.memBytes)
  )
}

// capabilitiesOf keeps the well-formed entries of a capabilities map and drops the rest.
function capabilitiesOf(v: unknown): Capabilities {
  const out: Capabilities = {}
  if (!isObj(v)) return out
  for (const [name, c] of Object.entries(v)) {
    if (isObj(c) && isStr(c.state)) out[name] = { state: c.state, detail: isStr(c.detail) ? c.detail : '' }
  }
  return out
}

export function isK8sEvent(v: unknown): v is K8sEvent {
  return isObj(v) && [v.id, v.type, v.reason, v.message, v.at].every(isStr)
}

export function isStep(v: unknown): v is Step {
  return isObj(v) && [v.at, v.actor, v.verb, v.object].every(isStr) && typeof v.derived === 'boolean'
}

// isChain needs an id and at least one step, every step well formed: a chain with one bad step
// is dropped whole, since replaying it with a gap would teach the wrong order.
export function isChain(v: unknown): v is Chain {
  return isObj(v) && isStr(v.id) && v.id !== '' && Array.isArray(v.steps) && v.steps.length > 0 && v.steps.every(isStep)
}

// stepOf keeps the known fields of a checked step and nothing else.
function stepOf({ at, actor, verb, object, derived }: Step): Step {
  return { at, actor, verb, object, derived }
}

// parseTelemetry covers the events that carry no entities: metrics, capabilities, Kubernetes
// Events and causal chains.
function parseTelemetry(type: string, v: Obj): StreamEvent | null {
  switch (type) {
    case 'metrics': {
      const sizeBy = (SIZE_BY as readonly unknown[]).includes(v.sizeBy) ? (v.sizeBy as SizeBy) : 'pods'
      return { type, sizeBy, samples: (Array.isArray(v.samples) ? v.samples : []).filter(isSample) }
    }
    case 'capabilities':
      return { type, capabilities: capabilitiesOf(v.capabilities) }
    case 'event':
      return isK8sEvent(v.k8sEvent) ? { type, event: v.k8sEvent } : null
    case 'chain':
      return isChain(v) ? { type, chain: { id: v.id, steps: v.steps.map(stepOf) } } : null
    default:
      return null
  }
}

// parseEvent turns one SSE message into a typed event, or null for types this page does not
// draw yet (flow) and for payloads that fail their guard.
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
      return { type, entities: ents.map(normalize), edges, capabilities: capabilitiesOf(v.capabilities) }
    }
    case 'upsert':
      return isEntity(v.entity) ? { type, entity: normalize(v.entity) } : null
    case 'edgeUpsert':
      return isEdge(v.edge) ? { type, edge: v.edge } : null
    case 'delete':
    case 'edgeDelete':
      return isStr(v.id) ? { type, id: v.id } : null
    default:
      return parseTelemetry(type, v)
  }
}
