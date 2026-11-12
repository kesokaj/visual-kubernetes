import { curvePoint, flowCurve, type Curve } from '../lib/flows'
import type { Graph } from '../lib/graph'
import type { Body } from './layout'
import { posOf, satPositions, type Pos } from './parts'
import type { Scene } from './scene'

// FlowPath is the route a traffic edge is drawn along: one bent curve src → dst, or two when the
// flow was addressed to a Service (plan D4/D5): src → Service, then Service → dst.
export type FlowPath = { first: Curve; second: Curve | null; split: number }

// flowPath splits t at d1/(d1+d2) of the straight-line lengths so particles keep an even speed
// across both legs. A zero-length route stays on the first curve.
export function flowPath(a: Pos, b: Pos, via: Pos | null): FlowPath {
  if (!via) return { first: flowCurve(a, b), second: null, split: 1 }
  const d1 = Math.hypot(via.x - a.x, via.y - a.y)
  const d2 = Math.hypot(b.x - via.x, b.y - via.y)
  const split = d1 + d2 > 0 ? d1 / (d1 + d2) : 1
  return { first: flowCurve(a, via), second: flowCurve(via, b), split }
}

export function pathPoint(p: FlowPath, t: number): Pos {
  if (!p.second || t <= p.split) return curvePoint(p.first, p.split > 0 ? Math.min(1, t / p.split) : 1)
  return curvePoint(p.second, (t - p.split) / (1 - p.split))
}

// strokePath adds the path's curves to the current canvas path.
export function strokePath(ctx: CanvasRenderingContext2D, p: FlowPath): void {
  for (const c of p.second ? [p.first, p.second] : [p.first]) {
    ctx.moveTo(c.a.x, c.a.y)
    ctx.quadraticCurveTo(c.c.x, c.c.y, c.b.x, c.b.y)
  }
}

// viaPoint is where a Service is drawn: its own body when it is an anchor (a loose satellite,
// internal/model/satellites.go D15), else its orbit slot on its drawn app. null when the page
// does not draw it (Services group off, folded, over budget, Only show) → the direct curve.
export function viaPoint(id: string | undefined, scene: Scene, graph: Graph, bodies: Map<string, Body>): Pos | null {
  if (!id) return null
  if (scene.anchors.has(id)) return posOf(bodies, id)
  const appId = graph.entities.get(id)?.appId
  const app = appId ? scene.anchors.get(appId) : undefined
  const at = appId ? posOf(bodies, appId) : null
  if (!app || !at) return null
  const i = app.sats.findIndex((s) => s.id === id)
  return i < 0 ? null : (satPositions(app, at)[i] ?? null)
}
