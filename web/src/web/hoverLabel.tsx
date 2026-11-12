import type { Graph } from '../lib/graph'
import type { Camera } from './camera'
import type { Body } from './layout'
import { bodyBottom } from './labels'
import { podPositions, posOf, satPositions, type Pos } from './parts'
import { SIZE, type Scene } from './scene'

// HoverLabel names whatever is under the pointer (plan D6/D11): a white card above the object
// with its name (14px) and what it is, `Kind · namespace` (12px, PRD R10). It is drawn in the
// screen-space label layer like the names, never as an HTML widget (PRD R6), and never takes
// the pointer.
const GAP = 8 // px between the object's top and the card
const PAD_X = 10
const NAME_W = 8 // px per character, a safe upper bound for 14px semibold
const SUB_W = 7 // px per character for 12px
const CARD_H = 40

type Target = { at: Pos; top: number; name: string; what: string }

// targetOf resolves a hovered data-id to where it is drawn (world units) and what to print.
function targetOf(id: string, scene: Scene, graph: Graph, bodies: Map<string, Body>): Target | null {
  const anchor = scene.anchors.get(id)
  if (anchor) {
    const at = posOf(bodies, id)
    return at ? { at, top: bodyBottom(anchor), ...textOf(anchor.entity) } : null
  }
  const e = graph.entities.get(id)
  const app = e?.appId ? scene.anchors.get(e.appId) : undefined
  const c = app ? posOf(bodies, app.id) : null
  if (!e || !app || !c) return null
  const pod = podPositions(app, c)[app.pods.findIndex((p) => p.id === id)]
  if (pod) return { at: pod, top: SIZE.pod / 2 + 2, ...textOf(e) }
  const sat = satPositions(app, c)[app.sats.findIndex((s) => s.id === id)]
  return sat ? { at: sat, top: SIZE.sat / 2, ...textOf(e) } : null
}

function textOf(e: { name: string; kind: string; namespace?: string }): { name: string; what: string } {
  return { name: e.name, what: [e.kind, e.namespace].filter(Boolean).join(' · ') }
}

type Props = { id: string | null; scene: Scene; graph: Graph; bodies: Map<string, Body>; cam: Camera }

export function HoverLabel({ id, scene, graph, bodies, cam }: Props) {
  const t = id ? targetOf(id, scene, graph, bodies) : null
  if (!id || !t) return null
  const w = Math.max(t.name.length * NAME_W, t.what.length * SUB_W) + PAD_X * 2
  const x = t.at.x * cam.k + cam.x
  const bottom = (t.at.y - t.top) * cam.k + cam.y - GAP
  return (
    <g className="hover-label" data-testid="hover-label" data-for={id} pointerEvents="none">
      <rect x={x - w / 2} y={bottom - CARD_H} width={w} height={CARD_H} rx={8} />
      <text className="lbl name" x={x} y={bottom - CARD_H + 18} textAnchor="middle">
        {t.name}
      </text>
      <text className="lbl sub" x={x} y={bottom - 8} textAnchor="middle">
        {t.what}
      </text>
    </g>
  )
}
