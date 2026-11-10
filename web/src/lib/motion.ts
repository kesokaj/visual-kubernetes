import type { Entity } from './wire'

// How long a pulse ring and a pod's trip along its thread last on screen.
export const PULSE_MS = 1400
export const TRAVEL_MS = 1800

export type Pulse = { n: number; at: number }
// Travel is a pod moving from the node it was placed on to its app, along their thread.
export type Travel = { pod: string; node: string; app: string; at: number }

const nodeId = (name: string) => `core/Node/_/${name}`

// Motion derives the short-lived encodings from deltas (PRD S2/S3): an app pulses when it
// scales, gains or loses a pod, or one of its pods changes state (a restart shows as a status
// change); a pod newly placed on a node travels along that node's thread. The snapshot never
// starts motion: only changes the audience can watch happen do.
export class Motion {
  readonly pulses = new Map<string, Pulse>()
  readonly travels = new Map<string, Travel>()

  observe(prev: Entity | undefined, next: Entity, now: number): void {
    // app/ ids are apps even when their kind is Pod (a bare Pod's own app, D23; BUG-001)
    if (next.kind === 'Pod' && !next.id.startsWith('app/')) {
      const app = next.appId
      if (!app) return
      if (!prev || prev.status !== next.status) this.pulse(app, now)
      if (next.nodeName && next.nodeName !== prev?.nodeName) {
        this.travels.set(next.id, { pod: next.id, node: nodeId(next.nodeName), app, at: now })
      }
      return
    }
    if (next.id.startsWith('app/') && (!prev || prev.status !== next.status)) this.pulse(next.id, now)
  }

  removed(prev: Entity | undefined, now: number): void {
    if (prev?.kind === 'Pod' && !prev.id.startsWith('app/') && prev.appId) this.pulse(prev.appId, now)
    if (prev) this.travels.delete(prev.id)
  }

  private pulse(app: string, now: number): void {
    this.pulses.set(app, { n: (this.pulses.get(app)?.n ?? 0) + 1, at: now })
  }

  // active reports whether anything still animates, and drops the travels that have arrived.
  active(now: number): boolean {
    for (const [id, t] of this.travels) if (now - t.at > TRAVEL_MS) this.travels.delete(id)
    if (this.travels.size > 0) return true
    for (const p of this.pulses.values()) if (now - p.at < PULSE_MS) return true
    return false
  }
}
