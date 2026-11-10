import type { Graph } from '../lib/graph'
import type { Edge, EdgeKind, Entity, FilterGroup } from '../lib/wire'

// Sizes in world units, from design/mockup-v3.html (apps 44, satellites 28, nodes 58, cp 54,
// pods 24 on a ring 48 beyond the app icon).
export const SIZE = { app: 44, sat: 28, node: 58, cp: 54, hub: 64, pod: 24, counter: 30 } as const
const POD_GAP = 30 // arc length per pod on the ring
// SAT_RADIUS is where app-owned satellites sit: between the app icon and the pod ring.
export const SAT_RADIUS = SIZE.app / 2 + 18

export const NODE_COLORS = [
  '#0d9488', '#7c3aed', '#ea580c', '#db2777', '#2563eb',
  '#65a30d', '#0891b2', '#9333ea', '#c2410c', '#be185d',
] as const

export type Role = 'hub' | 'cp' | 'node' | 'app' | 'sat' | 'counter'

// Anchor is one body the force layout moves. Pods and app-owned satellites ride on their app.
export type Anchor = {
  id: string
  entity: Entity
  role: Role
  icon: number // icon size
  ring: number // pod ring radius (apps), else 0
  r: number // body radius for collisions and the camera fit
  pods: Entity[]
  sats: Entity[]
}

export type Link = { id: string; from: string; to: string; kind: EdgeKind }

export type Scene = {
  anchors: Map<string, Anchor>
  links: Link[]
  nodeColor: Map<string, string>
}

function roleOf(e: Entity): Role | null {
  if (e.id === 'cp/apiserver') return 'hub'
  if (e.group === 'cp') return 'cp'
  if (e.kind === 'Node') return 'node'
  if (e.kind === 'Counter') return 'counter'
  if (e.kind === 'Pod') return null
  if (e.id.startsWith('app/')) return 'app'
  return 'sat'
}

function baseIcon(role: Role): number {
  return { hub: SIZE.hub, cp: SIZE.cp, node: SIZE.node, app: SIZE.app, sat: SIZE.sat, counter: SIZE.counter }[role]
}

// ringRadius grows with the pod count so pods never overlap on the ring.
export function ringRadius(pods: number): number {
  return Math.max(SIZE.app / 2 + 48, (pods * POD_GAP) / (2 * Math.PI))
}

function anchorFor(e: Entity, role: Role): Anchor {
  const icon = baseIcon(role)
  return { id: e.id, entity: e, role, icon, ring: 0, r: icon / 2 + 10, pods: [], sats: [] }
}

// buildScene applies the filter (plan D3): an entity shows when its group is on; pods and
// app-owned satellites also need their app; a link needs both ends drawn.
export function buildScene(
  g: Graph,
  on: ReadonlySet<FilterGroup>,
  palette: Map<string, string>,
): Scene {
  const anchors = new Map<string, Anchor>()
  const riders: Entity[] = []
  for (const e of g.entities.values()) {
    if (!on.has(e.filterGroup)) continue
    const role = roleOf(e)
    if (role === null || (role === 'sat' && e.appId)) riders.push(e)
    else anchors.set(e.id, anchorFor(e, role))
  }
  for (const e of riders) {
    const app = e.appId ? anchors.get(e.appId) : undefined
    if (!app) continue
    if (e.kind === 'Pod') app.pods.push(e)
    else app.sats.push(e)
  }
  const podsOn = on.has('pods')
  for (const a of anchors.values()) {
    if (a.role !== 'app') continue
    a.pods.sort((x, y) => x.id.localeCompare(y.id))
    a.sats.sort((x, y) => x.id.localeCompare(y.id))
    // With Pods hidden the ring leaves too, so apps shrink and the web closes up.
    a.ring = podsOn ? ringRadius(a.pods.length) : 0
    a.r = podsOn ? a.ring + SIZE.pod / 2 + 8 : SAT_RADIUS + SIZE.sat / 2 + 8
  }
  const links: Link[] = []
  for (const e of g.edges.values()) {
    if (anchors.has(e.from) && anchors.has(e.to)) links.push(linkOf(e))
  }
  links.sort((x, y) => x.id.localeCompare(y.id))
  return { anchors, links, nodeColor: nodeColors(g, palette) }
}

function linkOf(e: Edge): Link {
  return { id: e.id, from: e.from, to: e.to, kind: e.kind }
}

// nodeColors gives each node a colour the first time it is seen and keeps it for the session
// (palette is owned by the caller), so a node joining or a group being hidden never recolours
// the threads.
function nodeColors(g: Graph, palette: Map<string, string>): Map<string, string> {
  const names = [...g.entities.values()].filter((e) => e.kind === 'Node').map((e) => e.name).sort()
  for (const n of names) {
    if (!palette.has(n)) palette.set(n, NODE_COLORS[palette.size % NODE_COLORS.length] ?? NODE_COLORS[0])
  }
  return palette
}
