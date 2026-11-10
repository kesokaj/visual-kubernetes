import type { Edge, Entity, StreamEvent } from './wire'

// Graph is the page's copy of the live view: the snapshot plus every delta since. It is a
// plain external store (subscribe + version) so React reads it with useSyncExternalStore.
export class Graph {
  readonly entities = new Map<string, Entity>()
  readonly edges = new Map<string, Edge>()
  version = 0
  private readonly listeners = new Set<() => void>()

  apply(ev: StreamEvent): void {
    switch (ev.type) {
      case 'snapshot':
        this.entities.clear()
        this.edges.clear()
        for (const e of ev.entities) this.entities.set(e.id, e)
        for (const e of ev.edges) this.edges.set(e.id, e)
        break
      case 'upsert':
        this.entities.set(ev.entity.id, ev.entity)
        break
      case 'delete':
        this.entities.delete(ev.id)
        break
      case 'edgeUpsert':
        this.edges.set(ev.edge.id, ev.edge)
        break
      case 'edgeDelete':
        this.edges.delete(ev.id)
        break
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

// connect opens the same-origin stream and feeds the graph. EventSource reconnects by itself
// and sends Last-Event-ID, so the hub's resume ring (or a fresh snapshot) covers the gap.
export function connect(graph: Graph, parse: (type: string, data: string) => StreamEvent | null) {
  const es = new EventSource('/api/stream')
  const types = ['snapshot', 'upsert', 'delete', 'edgeUpsert', 'edgeDelete'] as const
  const handler = (msg: MessageEvent<string>) => {
    const ev = parse(msg.type, msg.data)
    if (ev) graph.apply(ev)
  }
  for (const t of types) es.addEventListener(t, handler)
  return () => es.close()
}
