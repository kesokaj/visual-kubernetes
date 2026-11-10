import type { Graph } from '../lib/graph'
import type { Edge, EdgeKind, Entity, FilterGroup, SizeBy } from '../lib/wire'
import { MAX_BUBBLES, foldedStatics, overBudgetCounter } from './folding'
import { sizeApps } from './sizing'

// Sizes in world units, from design/mockup-v3.html (apps 44, satellites 28, nodes 58, cp 54,
// pods 24 on a ring 48 beyond the app icon). App icons are resized by sizing.ts.
export const SIZE = { app: 44, sat: 28, node: 58, cp: 54, hub: 64, pod: 24 } as const
const POD_GAP = 30 // arc length per pod on the ring
const SAT_GAP = 18 // app-owned satellites sit this far beyond the app icon, inside the pod ring

export const NODE_COLORS = [
  '#0d9488', '#7c3aed', '#ea580c', '#db2777', '#2563eb',
  '#65a30d', '#0891b2', '#9333ea', '#c2410c', '#be185d',
] as const

export type Role = 'hub' | 'cp' | 'node' | 'app' | 'sat'

// Anchor is one body the force layout moves. Pods and app-owned satellites ride on their app.
export type Anchor = {
  id: string
  entity: Entity
  role: Role
  icon: number // icon size
  ring: number // pod ring radius (apps), else 0
  satR: number // satellite orbit radius (apps), else 0
  r: number // body radius for collisions and the camera fit
  pods: Entity[]
  sats: Entity[]
  measure: number // the sizeBy value (apps)
  top: number // 1–3 for the halo, else 0
}

export type Link = { id: string; from: string; to: string; kind: EdgeKind }

export type Scene = {
  anchors: Map<string, Anchor>
  links: Link[]
  nodeColor: Map<string, string>
  counters: Entity[] // corner counters: the model's counter/* plus the page's over-budget one
  statics: Map<string, string[]> // cp/* id → static-pod apps folded behind it (D30)
  sizeBy: SizeBy
}

function roleOf(e: Entity): Role | 'counter' | null {
  if (e.id === 'cp/apiserver') return 'hub'
  if (e.group === 'cp') return 'cp'
  if (e.kind === 'Node') return 'node'
  if (e.kind === 'Counter') return 'counter'
  // app/ first: a bare Pod's own app (D23) has kind Pod too, and must draw as an app
  if (e.id.startsWith('app/')) return 'app'
  if (e.kind === 'Pod') return null
  return 'sat'
}

function baseIcon(role: Role): number {
  return { hub: SIZE.hub, cp: SIZE.cp, node: SIZE.node, app: SIZE.app, sat: SIZE.sat }[role]
}

// ringRadius grows with the pod count so pods never overlap on the ring, and clears the icon.
export function ringRadius(pods: number, icon: number = SIZE.app): number {
  return Math.max(icon / 2 + 48, (pods * POD_GAP) / (2 * Math.PI))
}

function anchorFor(e: Entity, role: Role): Anchor {
  const icon = baseIcon(role)
  return { id: e.id, entity: e, role, icon, ring: 0, satR: 0, r: icon / 2 + 10, pods: [], sats: [], measure: 0, top: 0 }
}

// buildScene applies the filter (plan D3): an entity shows when its group is on; pods and
// app-owned satellites also need their app; a link needs both ends drawn. Folded entities
// (S5), static control-plane pods with a cp/* entity (D30) and bodies past the bubble budget
// are left out; counters go to the corner.
export function buildScene(g: Graph, on: ReadonlySet<FilterGroup>, palette: Map<string, string>): Scene {
  const statics = foldedStatics(g.entities)
  const hidden = new Set([...statics.values()].flat())
  const anchors = new Map<string, Anchor>()
  const riders: Entity[] = []
  const counters: Entity[] = []
  const podCount = new Map<string, number>()
  for (const e of g.entities.values()) {
    if (e.folded || hidden.has(e.id) || (e.appId && hidden.has(e.appId))) continue
    if (e.kind === 'Pod' && e.appId) podCount.set(e.appId, (podCount.get(e.appId) ?? 0) + 1)
    if (!on.has(e.filterGroup)) continue
    const role = roleOf(e)
    if (role === 'counter') counters.push(e)
    else if (role === null || (role === 'sat' && e.appId)) riders.push(e)
    else anchors.set(e.id, anchorFor(e, role))
  }
  for (const e of riders) {
    const app = e.appId ? anchors.get(e.appId) : undefined
    if (!app) continue
    if (e.kind === 'Pod') app.pods.push(e)
    else app.sats.push(e)
  }
  const apps = [...anchors.values()].filter((a) => a.role === 'app')
  const sizeBy = sizeApps(apps, g.metrics, podCount)
  const over = keepBudget(anchors)
  if (over > 0 && on.has('system')) counters.push(overBudgetCounter(over))
  shapeApps(anchors, on.has('pods'))
  const links: Link[] = []
  for (const e of g.edges.values()) {
    if (anchors.has(e.from) && anchors.has(e.to)) links.push(linkOf(e))
  }
  links.sort((x, y) => x.id.localeCompare(y.id))
  counters.sort((x, y) => x.id.localeCompare(y.id))
  return { anchors, links, nodeColor: nodeColors(g, palette), counters, statics, sizeBy }
}

// shapeApps sorts each app's riders and sets its rings from its (possibly resized) icon.
function shapeApps(anchors: Map<string, Anchor>, podsOn: boolean): void {
  for (const a of anchors.values()) {
    if (a.role !== 'app') continue
    a.pods.sort((x, y) => x.id.localeCompare(y.id))
    a.sats.sort((x, y) => x.id.localeCompare(y.id))
    a.satR = a.icon / 2 + SAT_GAP
    // With Pods hidden the ring leaves too, so apps shrink and the web closes up.
    a.ring = podsOn ? ringRadius(a.pods.length, a.icon) : 0
    a.r = podsOn ? a.ring + SIZE.pod / 2 + 8 : a.satR + SIZE.sat / 2 + 8
  }
}

// keepBudget drops the lowest-ranked bodies past MAX_BUBBLES and returns how many it dropped:
// the hub, control plane and nodes always stay, then apps by measure, then loose satellites.
function keepBudget(anchors: Map<string, Anchor>): number {
  if (anchors.size <= MAX_BUBBLES) return 0
  const rank = (a: Anchor) => (a.role === 'app' ? 1 : a.role === 'sat' ? 2 : 0)
  const order = [...anchors.values()].sort(
    (x, y) => rank(x) - rank(y) || y.measure - x.measure || y.pods.length - x.pods.length || x.id.localeCompare(y.id),
  )
  const drop = order.slice(MAX_BUBBLES)
  for (const a of drop) anchors.delete(a.id)
  return drop.length
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
