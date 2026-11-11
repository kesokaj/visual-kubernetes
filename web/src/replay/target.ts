import type { Graph } from '../lib/graph'
import type { Step } from '../lib/wire'
import type { Scene } from '../web/scene'

// HUB is the API server's anchor: every request passes it, so derived steps point there.
export const HUB = 'cp/apiserver'

// targetOf picks the drawn body a step's caption points at. Derived steps (the API server's
// request path) point at the hub. Otherwise the object itself when it is drawn, else the app
// it belongs to: a pod's appId, the app that stands for a workload (its ownerId), or the app of
// a pod the object owns (a ReplicaSet). Anything else, an EndpointSlice or a deleted pod,
// falls back to the hub so the caption is never lost off screen.
export function targetOf(step: Step, graph: Graph, scene: Scene): string {
  if (step.derived) return HUB
  const drawn = (id: string | undefined): id is string => id !== undefined && scene.anchors.has(id)
  if (drawn(step.object)) return step.object
  const appId = graph.entities.get(step.object)?.appId
  if (drawn(appId)) return appId
  for (const e of graph.entities.values()) {
    if (e.ownerId !== step.object) continue
    if (drawn(e.id)) return e.id
    if (drawn(e.appId)) return e.appId
  }
  return HUB
}

// objectLabel turns a D13 id (group/Kind/namespace/name, "_" for cluster-scoped) into what the
// audience reads: "Pod shop/web-7d4b9c-x002".
export function objectLabel(id: string): string {
  const [, kind = '', ns = '', ...rest] = id.split('/')
  const name = rest.join('/')
  if (!name) return id
  return ns === '_' ? `${kind} ${name}` : `${kind} ${ns}/${name}`
}

// clockOf shows a step's time as HH:MM:SS.mmm (UTC), the precision the chain carries.
export function clockOf(at: string): string {
  const m = /T(\d\d:\d\d:\d\d(?:\.\d{1,3})?)/.exec(at)
  return m?.[1] ?? at
}
