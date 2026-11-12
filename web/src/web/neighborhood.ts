import type { Graph } from '../lib/graph'
import type { Scene } from './scene'

// The hub is always part of a neighbourhood: every object reaches the cluster through it.
const HUB = 'cp/apiserver'

// Links followed upstream past the first hop, so an app also lights the Ingress, Gateway and
// ext/internet that route to it (plan D3).
const UPSTREAM = new Set(['routes', 'parent'])

// resolveAnchorId maps an entity id to the body it is drawn on: itself when it is an anchor,
// else its app when the app is drawn (a pod or an app-owned satellite), else null.
export function resolveAnchorId(id: string, scene: Scene, graph: Graph): string | null {
  if (scene.anchors.has(id)) return id
  const appId = graph.entities.get(id)?.appId
  return appId && scene.anchors.has(appId) ? appId : null
}

// neighborhood is the set of anchor ids that work together with id (the hovered or selected
// object): its own body, every body one link away, the routes/parent chain upstream of those,
// both ends (and the via Service's anchor) of every live traffic edge touching it, and the hub. null means "no neighbourhood":
// nothing is focused, or id is not drawn, so nothing fades.
export function neighborhood(scene: Scene, graph: Graph, id: string | null): Set<string> | null {
  if (!id) return null
  const self = resolveAnchorId(id, scene, graph)
  if (!self) return null
  const hl = new Set([self, HUB])
  for (const l of scene.links) {
    if (l.from === self) hl.add(l.to)
    else if (l.to === self) hl.add(l.from)
  }
  addUpstream(scene, hl)
  for (const e of graph.flows.edges.values()) {
    const src = resolveAnchorId(e.src, scene, graph)
    const dst = resolveAnchorId(e.dst, scene, graph)
    // The via Service counts as touching the edge too: a standalone Service lights both ends.
    const via = e.via ? resolveAnchorId(e.via, scene, graph) : null
    if (!src || !dst || (src !== self && dst !== self && via !== self)) continue
    hl.add(src)
    hl.add(dst)
    if (via) hl.add(via)
  }
  return hl
}

// addUpstream grows hl along routes/parent links toward their source until nothing changes.
function addUpstream(scene: Scene, hl: Set<string>): void {
  for (let grew = true; grew; ) {
    grew = false
    for (const l of scene.links) {
      if (UPSTREAM.has(l.kind) && hl.has(l.to) && !hl.has(l.from)) {
        hl.add(l.from)
        grew = true
      }
    }
  }
}

// fadedBy reports whether an anchor fades under neighbourhood hl (null: nothing fades).
export function fadedBy(hl: ReadonlySet<string> | null, id: string): boolean {
  return hl !== null && !hl.has(id)
}
