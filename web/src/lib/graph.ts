import { Chains } from './chains'
import { Flows } from './flows'
import { Motion } from './motion'
import { EventLog, Metrics } from './telemetry'
import type { Capabilities, Edge, Entity, StreamEvent } from './wire'

// Graph is the page's copy of the live view: the snapshot plus every delta since, the latest
// capabilities, metrics with a short history, recent Kubernetes Events, the causal chains the
// presenter can replay, traffic flows and the motion those deltas start. It is a plain external
// store (subscribe + version) so React reads it with useSyncExternalStore.
export class Graph {
  readonly entities = new Map<string, Entity>()
  readonly edges = new Map<string, Edge>()
  readonly metrics = new Metrics()
  readonly events = new EventLog()
  readonly chains = new Chains()
  readonly flows = new Flows()
  readonly motion = new Motion()
  caps: Capabilities = {}
  version = 0
  private readonly listeners = new Set<() => void>()
  private readonly now: () => number

  constructor(now: () => number = () => performance.now()) {
    this.now = now
  }

  apply(ev: StreamEvent): void {
    switch (ev.type) {
      case 'snapshot':
        this.entities.clear()
        this.edges.clear()
        this.flows.clear()
        for (const e of ev.entities) this.entities.set(e.id, e)
        for (const e of ev.edges) this.edges.set(e.id, e)
        this.caps = ev.capabilities
        break
      case 'upsert':
        this.motion.observe(this.entities.get(ev.entity.id), ev.entity, this.now())
        this.entities.set(ev.entity.id, ev.entity)
        break
      case 'delete':
        this.motion.removed(this.entities.get(ev.id), this.now())
        this.entities.delete(ev.id)
        this.metrics.forget(ev.id)
        break
      case 'edgeUpsert':
        this.edges.set(ev.edge.id, ev.edge)
        break
      case 'edgeDelete':
        this.edges.delete(ev.id)
        break
      case 'metrics':
        this.metrics.apply(ev.sizeBy, ev.samples)
        break
      case 'capabilities':
        this.caps = { ...this.caps, ...ev.capabilities }
        break
      case 'event':
        this.events.add(ev.event)
        break
      case 'chain':
        this.chains.add(ev.chain)
        break
      case 'flow':
        this.flows.add(ev.flow, this.now())
        return
    }
    this.version += 1
    for (const l of this.listeners) l()
  }

  subscribe = (l: () => void): (() => void) => {
    this.listeners.add(l)
    return () => this.listeners.delete(l)
  }

  getVersion = (): number => this.version
}

export const STREAM_TYPES = [
  'snapshot',
  'upsert',
  'delete',
  'edgeUpsert',
  'edgeDelete',
  'metrics',
  'capabilities',
  'event',
  'chain',
  'flow',
] as const

// connect opens the same-origin stream and feeds the graph. EventSource reconnects by itself
// and sends Last-Event-ID, so the hub's resume ring (or a fresh snapshot) covers the gap.
export function connect(graph: Graph, parse: (type: string, data: string) => StreamEvent | null) {
  const es = new EventSource('/api/stream')
  const handler = (msg: MessageEvent<string>) => {
    const ev = parse(msg.type, msg.data)
    if (ev) graph.apply(ev)
  }
  for (const t of STREAM_TYPES) es.addEventListener(t, handler)
  return () => es.close()
}
