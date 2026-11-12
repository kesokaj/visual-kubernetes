import type { RefObject } from 'react'
import { DROP_AT, DROP_MS } from '../lib/flows'
import type { Graph } from '../lib/graph'
import type { Camera } from './camera'
import { flowPath, pathPoint, strokePath, viaPoint, type FlowPath } from './flowPath'
import type { Body } from './layout'
import { fadedBy, resolveAnchorId } from './neighborhood'
import { posOf, type Pos } from './parts'
import type { Scene } from './scene'
import { useTrafficCanvas } from './useTrafficCanvas'

type Props = {
  graph: Graph
  scene: Scene
  bodies: Map<string, Body>
  camRef: RefObject<Camera>
  size: { w: number; h: number }
  visible: boolean
  hl: ReadonlySet<string> | null // neighbourhood fade: endpoints outside it draw like dimmed ones
}

// View is what every draw pass reads: the graph, the scene, the body positions and the
// neighbourhood fade.
type View = { graph: Graph; scene: Scene; bodies: Map<string, Body>; hl: ReadonlySet<string> | null }

function endpointOf(id: string, v: View): { pos: Pos; sys: boolean; dimmed: boolean } | null {
  const aid = resolveAnchorId(id, v.scene, v.graph)
  if (!aid) return null
  const pos = posOf(v.bodies, aid)
  const anchor = v.scene.anchors.get(aid)
  return pos && anchor
    ? {
        pos,
        sys: !v.scene.focused && anchor.entity.filterGroup === 'system',
        dimmed: anchor.dimmed || fadedBy(v.hl, aid),
      }
    : null
}

// pathOf routes src → dst through the via Service when the page draws it, else directly.
function pathOf(from: Pos, to: Pos, via: string | undefined, v: View): FlowPath {
  return flowPath(from, to, viaPoint(via, v.scene, v.graph, v.bodies))
}

function drawEdges(ctx: CanvasRenderingContext2D, v: View) {
  let flows = 0
  let drops = 0
  let vias = 0
  ctx.lineWidth = 1.4
  for (const e of v.graph.flows.edges.values()) {
    const from = endpointOf(e.src, v)
    const to = endpointOf(e.dst, v)
    if (!from || !to) continue
    flows += 1
    if (e.verdict === 'DROPPED') drops += 1
    const path = pathOf(from.pos, to.pos, e.via, v)
    if (path.second) vias += 1
    ctx.setLineDash(e.verdict === 'DROPPED' ? [5, 4] : [])
    ctx.strokeStyle = e.verdict === 'DROPPED' ? '#fca5a5' : '#cbd5e1'
    ctx.globalAlpha =
      from.dimmed || to.dimmed ? 0.12 : from.sys ? 0.4 : e.verdict === 'DROPPED' ? 0.85 : 0.7
    ctx.beginPath()
    strokePath(ctx, path)
    ctx.stroke()
  }
  ctx.setLineDash([])
  return { flows, drops, vias }
}

function drawParticles(ctx: CanvasRenderingContext2D, v: View) {
  for (const p of v.graph.flows.particles) {
    const from = endpointOf(p.src, v)
    const to = endpointOf(p.dst, v)
    if (!from || !to) continue
    const pt = pathPoint(pathOf(from.pos, to.pos, p.via, v), p.t)
    ctx.globalAlpha = from.dimmed || to.dimmed ? 0.14 : from.sys ? 0.4 : 0.95
    ctx.fillStyle = p.verdict === 'DROPPED' ? '#dc2626' : '#3b82f6'
    ctx.beginPath()
    ctx.arc(pt.x, pt.y, p.verdict === 'DROPPED' ? 3 : 2.8, 0, Math.PI * 2)
    ctx.fill()
  }
}

function drawDrops(ctx: CanvasRenderingContext2D, v: View, now: number) {
  const s = 6
  ctx.strokeStyle = '#dc2626'
  ctx.lineWidth = 2.4
  for (const d of v.graph.flows.drops) {
    const from = endpointOf(d.src, v)
    const to = endpointOf(d.dst, v)
    if (!from || !to) continue
    const pt = pathPoint(pathOf(from.pos, to.pos, d.via, v), DROP_AT)
    const fade = Math.max(0, 1 - (now - d.at) / DROP_MS)
    ctx.globalAlpha = from.dimmed || to.dimmed ? fade * 0.18 : fade
    ctx.beginPath()
    ctx.moveTo(pt.x - s, pt.y - s)
    ctx.lineTo(pt.x + s, pt.y + s)
    ctx.moveTo(pt.x + s, pt.y - s)
    ctx.lineTo(pt.x - s, pt.y + s)
    ctx.stroke()
  }
}

function paintFrame(
  graph: Graph,
  hl: ReadonlySet<string> | null,
  ctx: CanvasRenderingContext2D,
  cv: HTMLCanvasElement,
  sc: Scene,
  bd: Map<string, Body>,
  cam: Camera,
  dpr: number,
  now: number,
) {
  const v: View = { graph, scene: sc, bodies: bd, hl }
  graph.flows.step(now, (s, d) => endpointOf(s, v) !== null && endpointOf(d, v) !== null)
  ctx.setTransform(1, 0, 0, 1, 0, 0)
  ctx.clearRect(0, 0, cv.width, cv.height)
  ctx.setTransform(dpr * cam.k, 0, 0, dpr * cam.k, dpr * cam.x, dpr * cam.y)
  const counts = drawEdges(ctx, v)
  drawParticles(ctx, v)
  drawDrops(ctx, v, now)
  ctx.globalAlpha = 1
  cv.dataset.flows = String(counts.flows)
  cv.dataset.drops = String(counts.drops)
  cv.dataset.via = String(counts.vias)
}

// TrafficCanvas draws live traffic edges, particles and policy-drop marks on a 2D canvas over
// the SVG web (ADR-003). Its animation loop sleeps whenever no flow is active or Traffic is off.
// The paint closure is refreshed every render, so a new neighbourhood reaches a running loop.
export function TrafficCanvas({ graph, scene, bodies, camRef, size, visible, hl }: Props) {
  const cvRef = useTrafficCanvas({
    graph,
    scene,
    bodies,
    camRef,
    size,
    visible,
    paint: (ctx, cv, sc, bd, cam, dpr, now) => paintFrame(graph, hl, ctx, cv, sc, bd, cam, dpr, now),
  })
  return <canvas ref={cvRef} className="fx" data-testid="traffic-canvas" data-flows="0" data-drops="0" data-via="0" aria-hidden="true" />
}
