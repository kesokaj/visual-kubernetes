import type { K8sEvent, Sample, SizeBy } from './wire'

// HISTORY is how many metrics samples each id keeps for its sparklines: 18 × 5 s = 90 s.
export const HISTORY = 18
// EVENTS_KEPT bounds the page's Event log so a noisy cluster cannot grow it without limit.
const EVENTS_KEPT = 500

// Metrics keeps the latest sample per id and a short history for the panel's sparklines.
// sizeBy starts at 'pods': until the first metrics event the bubbles fall back to pod count.
export class Metrics {
  sizeBy: SizeBy = 'pods'
  readonly latest = new Map<string, Sample>()
  readonly history = new Map<string, Sample[]>()

  apply(sizeBy: SizeBy, samples: readonly Sample[]): void {
    this.sizeBy = sizeBy
    this.latest.clear()
    for (const s of samples) {
      this.latest.set(s.id, s)
      const h = this.history.get(s.id) ?? []
      h.push(s)
      if (h.length > HISTORY) h.splice(0, h.length - HISTORY)
      this.history.set(s.id, h)
    }
  }

  forget(id: string): void {
    this.latest.delete(id)
    this.history.delete(id)
  }
}

// SizeMetric is what app bubbles can be sized by on the page (PRD C3): the server's sizeBy
// choices plus memory, which the server samples but never picks on its own.
export const SIZE_METRICS = ['network', 'cpu', 'memory', 'pods'] as const
export type SizeMetric = SizeBy | 'memory'

// measureOf is the value a sample contributes under a metric; pods are counted by the scene.
export function measureOf(s: Sample | undefined, metric: SizeMetric): number {
  if (!s) return 0
  if (metric === 'network') return (s.rxBps ?? 0) + (s.txBps ?? 0)
  if (metric === 'cpu') return s.cpuMilli ?? 0
  if (metric === 'memory') return s.memBytes ?? 0
  return 0
}

// metricAvailable reports whether the latest samples carry a metric at all; pods always do.
export function metricAvailable(m: Metrics, metric: SizeMetric): boolean {
  if (metric === 'pods') return true
  for (const s of m.latest.values()) {
    if (metric === 'network' && (s.rxBps !== undefined || s.txBps !== undefined)) return true
    if (metric === 'cpu' && s.cpuMilli !== undefined) return true
    if (metric === 'memory' && s.memBytes !== undefined) return true
  }
  return false
}

// EventLog keeps the newest Kubernetes Events, de-duplicated so a replayed or resent Event is
// listed once.
export class EventLog {
  private items: K8sEvent[] = []
  private readonly seen = new Set<string>()

  add(e: K8sEvent): void {
    const key = `${e.id}|${e.at}|${e.reason}|${e.message}`
    if (this.seen.has(key)) return
    this.seen.add(key)
    this.items.push(e)
    if (this.items.length > EVENTS_KEPT) {
      const drop = this.items.splice(0, this.items.length - EVENTS_KEPT)
      for (const d of drop) this.seen.delete(`${d.id}|${d.at}|${d.reason}|${d.message}`)
    }
  }

  // about lists the Events about any of ids, newest first.
  about(ids: ReadonlySet<string>, limit = 20): K8sEvent[] {
    const out: K8sEvent[] = []
    for (let i = this.items.length - 1; i >= 0 && out.length < limit; i--) {
      const e = this.items[i]
      if (e && ids.has(e.id)) out.push(e)
    }
    return out
  }
}
