import {
  forceCollide,
  forceLink,
  forceManyBody,
  forceSimulation,
  forceX,
  forceY,
  type Simulation,
  type SimulationLinkDatum,
  type SimulationNodeDatum,
} from 'd3-force'
import type { Anchor, Link, Scene } from './scene'

export type Body = SimulationNodeDatum & { id: string; r: number; collideR: number; role: Anchor['role'] }
type BodyLink = SimulationLinkDatum<Body> & { kind: Link['kind'] }

// Link rest lengths and pulls (plan D2, D54, ADR-003): airy rest lengths keep control-plane
// parts, worker nodes and workloads well clear of each other and their screen-space labels.
const LINK = {
  member: { distance: 340, strength: 0.4 },
  hosts: { distance: 480, strength: 0.05 },
  selects: { distance: 240, strength: 0.25 },
  references: { distance: 240, strength: 0.25 },
  routes: { distance: 260, strength: 0.25 },
  parent: { distance: 210, strength: 0.32 },
} as const

// Layout owns one d3-force simulation of the anchors. update() keeps every surviving body's
// position and velocity, so the web reshapes instead of jumping when the cluster changes.
export class Layout {
  readonly bodies = new Map<string, Body>()
  private linkKey = ''
  private readonly sim: Simulation<Body, BodyLink>
  private readonly links = forceLink<Body, BodyLink>()
    .id((b) => b.id)
    .distance((l) => LINK[l.kind].distance)
    .strength((l) => LINK[l.kind].strength)

  constructor(onTick: () => void) {
    this.sim = forceSimulation<Body, BodyLink>()
      .force('charge', forceManyBody<Body>().strength((b) => -650 - b.collideR * 8).distanceMax(2200))
      .force('collide', forceCollide<Body>((b) => b.collideR).strength(0.95).iterations(3))
      .force('link', this.links)
      // A weaker pull on x than on y spreads the web wide across the 16:9 screen without squashing labels.
      .force('x', forceX<Body>(0).strength(0.022))
      .force('y', forceY<Body>(0).strength(0.042))
      .alphaDecay(0.025)
      .on('tick', onTick)
  }

  update(scene: Scene): void {
    const next = new Map<string, Body>()
    let grown = false // a body or link appeared or left
    let resized = false // only radii changed (a metrics cycle resizing apps)
    for (const a of scene.anchors.values()) {
      const old = this.bodies.get(a.id)
      const b = old ?? this.spawn(a, scene)
      if (!old) grown = true
      else if (Math.abs(b.r - a.r) > 12) grown = true // a ring appeared or left (Pods filter)
      else if (Math.abs(b.r - a.r) > 0.5 || Math.abs(b.collideR - a.collideR) > 0.5) resized = true
      b.r = a.r
      b.collideR = a.collideR
      b.role = a.role
      next.set(a.id, b)
    }
    if (next.size !== this.bodies.size) grown = true
    const linkKey = scene.links.map((l) => l.id).join('\n')
    if (linkKey !== this.linkKey) grown = true
    this.linkKey = linkKey
    this.bodies.clear()
    for (const [id, b] of next) this.bodies.set(id, b)
    const hub = this.bodies.get('cp/apiserver')
    if (hub) {
      hub.fx = 0 // the hub pins the web's centre so it never drifts off screen
      hub.fy = 0
    }
    this.sim.nodes([...this.bodies.values()])
    this.links.links(scene.links.map((l) => ({ source: l.from, target: l.to, kind: l.kind })))
    // A changed cluster reheats the web; a resize only nudges it, so a metrics cycle every
    // 5 s does not keep the layout boiling. A status-only or highlight-only change moves nothing.
    if (grown) this.sim.alpha(Math.max(this.sim.alpha(), 0.55)).restart()
    else if (resized) this.sim.alpha(Math.max(this.sim.alpha(), 0.12)).restart()
  }

  stop(): void {
    this.sim.stop()
  }

  // spawn places a new body next to something it links to (a pod's node, a node's hub), so it
  // grows out of the web instead of flying in from the centre.
  private spawn(a: Anchor, scene: Scene): Body {
    const near = scene.links
      .filter((l) => l.to === a.id || l.from === a.id)
      .map((l) => this.bodies.get(l.to === a.id ? l.from : l.to))
      .find((b) => b !== undefined)
    const angle = hashAngle(a.id)
    const dist = near ? 180 : 340
    return {
      id: a.id,
      r: a.r,
      collideR: a.collideR,
      role: a.role,
      x: (near?.x ?? 0) + Math.cos(angle) * dist,
      y: (near?.y ?? 0) + Math.sin(angle) * dist,
    }
  }
}

// hashAngle spreads new bodies around their neighbour by id, so reloads place them alike.
function hashAngle(id: string): number {
  let h = 2166136261
  for (let i = 0; i < id.length; i++) h = Math.imul(h ^ id.charCodeAt(i), 16777619)
  return ((h >>> 0) / 4294967296) * Math.PI * 2
}
