import type { RefObject } from 'react'
import { curvePoint, DROP_AT, DROP_MS, flowCurve } from '../lib/flows'
import type { Graph } from '../lib/graph'
import type { Camera } from './camera'
import type { Body } from './layout'
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
}

function resolveAnchorId(id: string, scene: Scene, graph: Graph): string | null {
  if (scene.anchors.has(id)) return id
  const appId = graph.entities.get(id)?.appId
  return appId && scene.anchors.has(appId) ? appId : null
}

function endpointOf(id: string, scene: Scene, graph: Graph, bodies: Map<string, Body>): { pos: Pos; sys: boolean } | null {
  const aid = resolveAnchorId(id, scene, graph)
  if (!aid) return null
  const pos = posOf(bodies, aid)
  const anchor = scene.anchors.get(aid)
  return pos && anchor ? { pos, sys: anchor.entity.filterGroup === 'system' } : null
}

function drawEdges(ctx: CanvasRenderingContext2D, graph: Graph, scene: Scene, bodies: Map<string, Body>) {
  let flows = 0
  let drops = 0
  ctx.lineWidth = 1.4
  for (const e of graph.flows.edges.values()) {
    const from = endpointOf(e.src, scene, graph, bodies)
    const to = endpointOf(e.dst, scene, graph, bodies)
    if (!from || !to) continue
    flows += 1
    if (e.verdict === 'DROPPED') drops += 1
    const { a, b, c } = flowCurve(from.pos, to.pos)
    ctx.setLineDash(e.verdict === 'DROPPED' ? [5, 4] : [])
    ctx.strokeStyle = e.verdict === 'DROPPED' ? '#fca5a5' : '#cbd5e1'
    ctx.globalAlpha = from.sys ? 0.4 : e.verdict === 'DROPPED' ? 0.85 : 0.7
    ctx.beginPath()
    ctx.moveTo(a.x, a.y)
    ctx.quadraticCurveTo(c.x, c.y, b.x, b.y)
    ctx.stroke()
  }
  ctx.setLineDash([])
  return { flows, drops }
}

function drawParticles(ctx: CanvasRenderingContext2D, graph: Graph, scene: Scene, bodies: Map<string, Body>) {
  for (const p of graph.flows.particles) {
    const from = endpointOf(p.src, scene, graph, bodies)
    const to = endpointOf(p.dst, scene, graph, bodies)
    if (!from || !to) continue
    const pt = curvePoint(flowCurve(from.pos, to.pos), p.t)
    ctx.globalAlpha = from.sys ? 0.4 : 0.95
    ctx.fillStyle = p.verdict === 'DROPPED' ? '#dc2626' : '#3b82f6'
    ctx.beginPath()
    ctx.arc(pt.x, pt.y, p.verdict === 'DROPPED' ? 3 : 2.8, 0, Math.PI * 2)
    ctx.fill()
  }
}

function drawDrops(ctx: CanvasRenderingContext2D, graph: Graph, scene: Scene, bodies: Map<string, Body>, now: number) {
  const s = 6
  ctx.strokeStyle = '#dc2626'
  ctx.lineWidth = 2.4
  for (const d of graph.flows.drops) {
    const from = endpointOf(d.src, scene, graph, bodies)
    const to = endpointOf(d.dst, scene, graph, bodies)
    if (!from || !to) continue
    const pt = curvePoint(flowCurve(from.pos, to.pos), DROP_AT)
    ctx.globalAlpha = Math.max(0, 1 - (now - d.at) / DROP_MS)
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
  ctx: CanvasRenderingContext2D,
  cv: HTMLCanvasElement,
  sc: Scene,
  bd: Map<string, Body>,
  cam: Camera,
  dpr: number,
  now: number,
) {
  graph.flows.step(now, (s, d) => endpointOf(s, sc, graph, bd) !== null && endpointOf(d, sc, graph, bd) !== null)
  ctx.setTransform(1, 0, 0, 1, 0, 0)
  ctx.clearRect(0, 0, cv.width, cv.height)
  ctx.setTransform(dpr * cam.k, 0, 0, dpr * cam.k, dpr * cam.x, dpr * cam.y)
  const counts = drawEdges(ctx, graph, sc, bd)
  drawParticles(ctx, graph, sc, bd)
  drawDrops(ctx, graph, sc, bd, now)
  ctx.globalAlpha = 1
  cv.dataset.flows = String(counts.flows)
  cv.dataset.drops = String(counts.drops)
}

// TrafficCanvas draws live traffic edges, particles and policy-drop marks on a 2D canvas over
// the SVG web (ADR-003). Its animation loop sleeps whenever no flow is active or Traffic is off.
export function TrafficCanvas({ graph, scene, bodies, camRef, size, visible }: Props) {
  const cvRef = useTrafficCanvas({
    graph,
    scene,
    bodies,
    camRef,
    size,
    visible,
    paint: (ctx, cv, sc, bd, cam, dpr, now) => paintFrame(graph, ctx, cv, sc, bd, cam, dpr, now),
  })
  return <canvas ref={cvRef} className="fx" data-testid="traffic-canvas" data-flows="0" data-drops="0" aria-hidden="true" />
}
