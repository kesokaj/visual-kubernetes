import type { Flow, FlowVerdict } from './wire'

// Active traffic state for the Canvas 2D overlay (design/mockup-v3.html:1056-1121, ADR-003).
// Each `flow` SSE message refreshes an edge for FLOW_TTL_MS so particles ride smoothly between
// 1–5 s backend flushes and vanish once traffic stops.
export const FLOW_TTL_MS = 6500
export const DROP_MS = 1200
export const DROP_AT = 0.82
export const MAX_PARTICLES = 600

export type ActiveFlow = {
  key: string
  src: string
  dst: string
  verdict: FlowVerdict
  rate: number
  acc: number
  until: number
}

export type Particle = {
  src: string
  dst: string
  verdict: FlowVerdict
  t: number
  speed: number
}

export type DropMark = {
  src: string
  dst: string
  at: number
}

export type Curve = {
  a: { x: number; y: number }
  b: { x: number; y: number }
  c: { x: number; y: number }
}

const BEND = 0.12

// flowCurve bends src->dst slightly to one side so bidirectional traffic separates cleanly.
export function flowCurve(a: { x: number; y: number }, b: { x: number; y: number }): Curve {
  const mx = (a.x + b.x) / 2
  const my = (a.y + b.y) / 2
  const dx = b.x - a.x
  const dy = b.y - a.y
  return { a, b, c: { x: mx - dy * BEND, y: my + dx * BEND } }
}

export function curvePoint({ a, b, c }: Curve, t: number): { x: number; y: number } {
  const u = 1 - t
  return {
    x: u * u * a.x + 2 * u * t * c.x + t * t * b.x,
    y: u * u * a.y + 2 * u * t * c.y + t * t * b.y,
  }
}

function particleRate(flow: Flow): number {
  const count = flow.count && flow.count > 0 ? flow.count : 1
  return Math.min(8, 1.8 + Math.log2(count + 1) * 1.4)
}

export class Flows {
  readonly edges = new Map<string, ActiveFlow>()
  particles: Particle[] = []
  drops: DropMark[] = []
  private readonly listeners = new Set<() => void>()
  private lastAt = 0
  private seq = 0

  subscribe = (l: () => void): (() => void) => {
    this.listeners.add(l)
    return () => this.listeners.delete(l)
  }

  private notify(): void {
    for (const l of this.listeners) l()
  }

  add(flow: Flow, now: number): void {
    const key = `${flow.src}->${flow.dst}:${flow.verdict}`
    const prev = this.edges.get(key)
    this.edges.set(key, {
      key,
      src: flow.src,
      dst: flow.dst,
      verdict: flow.verdict,
      rate: particleRate(flow),
      // Seed acc = 1 on a newly active edge so the first particle spawns on the next frame.
      acc: prev ? prev.acc : 1,
      until: now + FLOW_TTL_MS,
    })
    this.notify()
  }

  clear(): void {
    this.edges.clear()
    this.clearInFlight()
    this.notify()
  }

  clearInFlight(): void {
    this.particles = []
    this.drops = []
    this.lastAt = 0
  }

  active(now: number): boolean {
    for (const [k, e] of this.edges) if (now >= e.until) this.edges.delete(k)
    this.drops = this.drops.filter((d) => now - d.at < DROP_MS)
    return this.edges.size > 0 || this.particles.length > 0 || this.drops.length > 0
  }

  step(now: number, canDraw: (src: string, dst: string) => boolean): void {
    const dt = this.lastAt > 0 ? Math.min(0.1, Math.max(0, (now - this.lastAt) / 1000)) : 1 / 60
    this.lastAt = now
    for (const [k, e] of this.edges) {
      if (now >= e.until) {
        this.edges.delete(k)
        continue
      }
      if (!canDraw(e.src, e.dst)) continue
      e.acc += dt * e.rate
      const spawn = Math.floor(e.acc)
      e.acc -= spawn
      for (let i = 0; i < spawn && this.particles.length < MAX_PARTICLES; i++) {
        const speed = 0.42 + (this.seq++ % 5) * 0.03
        this.particles.push({ src: e.src, dst: e.dst, verdict: e.verdict, t: 0, speed })
      }
    }
    const kept: Particle[] = []
    for (const p of this.particles) {
      if (!canDraw(p.src, p.dst)) continue
      p.t += p.speed * dt
      if (p.verdict === 'DROPPED' && p.t >= DROP_AT) {
        this.drops.push({ src: p.src, dst: p.dst, at: now })
        continue
      }
      if (p.t < 1) kept.push(p)
    }
    this.particles = kept
    this.drops = this.drops.filter((d) => now - d.at < DROP_MS && canDraw(d.src, d.dst))
  }
}
