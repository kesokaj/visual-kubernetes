import {
  hasFocusFilter,
  isSelfEntity,
  matchesApp,
  matchesLooseSat,
  type FilterState,
} from '../filter/state'
import type { Graph } from '../lib/graph'
import type { Edge, EdgeKind, Entity, SizeBy } from '../lib/wire'
import { MAX_BUBBLES, foldedStatics, overBudgetCounter } from './folding'
import { sizeApps } from './sizing'

// Sizes in world units, from design/mockup-v3.html (apps 44, satellites 28, nodes 58, cp 54,
// pods 24 on a ring 48 beyond the app icon, the internet globe 54). App icons are resized by
// sizing.ts.
export const SIZE = { app: 44, sat: 28, node: 58, cp: 54, hub: 64, pod: 24, ext: 54 } as const
const POD_GAP = 30 // arc length per pod on the ring
const SAT_GAP = 18 // app-owned satellites sit this far beyond the app icon, inside the pod ring

export const NODE_COLORS = [
  '#0d9488', '#7c3aed', '#ea580c', '#db2777', '#2563eb',
  '#65a30d', '#0891b2', '#9333ea', '#c2410c', '#be185d',
] as const

// ext is ext/internet: the model draws it only while an entrypoint exists (plan D4).
export type Role = 'hub' | 'cp' | 'node' | 'app' | 'sat' | 'ext'

// Anchor is one body the force layout moves. Pods and app-owned satellites ride on their app.
export type Anchor = {
  id: string
  entity: Entity
  role: Role
  icon: number // icon size
  ring: number // pod ring radius (apps), else 0
  satR: number // satellite orbit radius (apps), else 0
  r: number // body radius for the camera fit
  collideR: number // collision clearance radius including screen-space label room
  pods: Entity[]
  sats: Entity[]
  measure: number // the sizeBy value (apps)
  top: number // 1–3 for the halo, else 0
  dimmed: boolean // dimmed in place when a Highlight focus filter does not match
}

export type Link = { id: string; from: string; to: string; kind: EdgeKind }

export type Scene = {
  anchors: Map<string, Anchor>
  links: Link[]
  nodeColor: Map<string, string>
  counters: Entity[] // corner counters: the model's counter/* plus the page's over-budget one
  statics: Map<string, string[]> // cp/* id → static-pod apps folded behind it (D30)
  sizeBy: SizeBy
  focused: boolean
}

function roleOf(e: Entity): Role | 'counter' | null {
  if (e.id === 'cp/apiserver') return 'hub'
  if (e.group === 'cp') return 'cp'
  if (e.kind === 'Node') return 'node'
  if (e.kind === 'Counter') return 'counter'
  if (e.group === 'ext') return 'ext'
  // app/ first: a bare Pod's own app (D23) has kind Pod too, and must draw as an app
  if (e.id.startsWith('app/')) return 'app'
  if (e.kind === 'Pod') return null
  return 'sat'
}

function baseIcon(role: Role): number {
  return { hub: SIZE.hub, cp: SIZE.cp, node: SIZE.node, app: SIZE.app, sat: SIZE.sat, ext: SIZE.ext }[role]
}

// ringRadius grows with the pod count so pods never overlap on the ring, and clears the icon.
export function ringRadius(pods: number, icon: number = SIZE.app): number {
  return Math.max(icon / 2 + 48, (pods * POD_GAP) / (2 * Math.PI))
}

function collideClearance(e: Entity, role: Role, r: number): number {
  const sub =
    role === 'app'
      ? [e.namespace, e.status].filter(Boolean).join(' · ')
      : role === 'sat'
        ? e.kind
        : e.status
  const labelSpan = Math.min(165, Math.max(e.name.length * 5.8, sub.length * 4.8))
  return Math.max(r + 62, 96, labelSpan + 18)
}

function anchorFor(e: Entity, role: Role): Anchor {
  const icon = baseIcon(role)
  const r = icon / 2 + 10
  return {
    id: e.id,
    entity: e,
    role,
    icon,
    ring: 0,
    satR: 0,
    r,
    collideR: collideClearance(e, role, r),
    pods: [],
    sats: [],
    measure: 0,
    top: 0,
    dimmed: false,
  }
}

function applyFocus(anchors: Map<string, Anchor>, edges: readonly Edge[], filter: FilterState): void {
  const matchedApps = new Set<string>()
  const matchedSats = new Set<string>()
  for (const a of anchors.values()) {
    if (a.role === 'app' && matchesApp(a.entity, filter)) matchedApps.add(a.id)
  }
  for (const a of anchors.values()) {
    if (a.role === 'sat' && matchesLooseSat(a.entity, matchedApps, edges, filter)) matchedSats.add(a.id)
  }
  if (filter.mode === 'highlight') {
    for (const a of anchors.values()) {
      a.dimmed = !matchedApps.has(a.id) && !matchedSats.has(a.id)
    }
    return
  }
  for (const [id, a] of anchors) {
    if (a.role === 'app' && !matchedApps.has(id)) anchors.delete(id)
    else if (a.role === 'sat' && !matchedSats.has(id)) anchors.delete(id)
    else if (a.role === 'cp') anchors.delete(id)
  }
  const hostedNodes = new Set<string>()
  for (const e of edges) {
    if (e.kind === 'hosts' && anchors.has(e.to)) hostedNodes.add(e.from)
  }
  for (const [id, a] of anchors) {
    if (a.role === 'node' && !hostedNodes.has(id)) anchors.delete(id)
  }
}

// pruneGlobe drops ext/internet unless it still routes to something shown: under a focus
// filter, a group switch or a hidden self app alike (review DoD finding 3).
function pruneGlobe(anchors: Map<string, Anchor>, edges: readonly Edge[]): void {
  for (const [id, a] of anchors) {
    if (a.role === 'ext' && !edges.some((e) => e.from === id && e.kind === 'routes' && anchors.has(e.to))) anchors.delete(id)
  }
}

// buildScene applies group switches, self-visibility and focus filters (plan D3, D54).
export function buildScene(g: Graph, filter: FilterState, palette: Map<string, string>): Scene {
  const statics = foldedStatics(g.entities)
  const hidden = new Set([...statics.values()].flat())
  const anchors = new Map<string, Anchor>()
  const riders: Entity[] = []
  const counters: Entity[] = []
  const podCount = new Map<string, number>()
  const focused = hasFocusFilter(filter)
  for (const e of g.entities.values()) {
    if (e.folded || hidden.has(e.id) || (e.appId && hidden.has(e.appId))) continue
    if (!filter.showSelf && isSelfEntity(e, g.entities)) continue
    if (e.kind === 'Pod' && e.appId) podCount.set(e.appId, (podCount.get(e.appId) ?? 0) + 1)
    if (!filter.groups.has(e.filterGroup)) continue
    const role = roleOf(e)
    if (role === 'counter') {
      if (!(focused && filter.mode === 'only')) counters.push(e)
    } else if (role === null || (role === 'sat' && e.appId)) {
      riders.push(e)
    } else {
      anchors.set(e.id, anchorFor(e, role))
    }
  }
  const allEdges = [...g.edges.values()]
  if (focused) applyFocus(anchors, allEdges, filter)
  pruneGlobe(anchors, allEdges)
  for (const e of riders) {
    const app = e.appId ? anchors.get(e.appId) : undefined
    if (!app) continue
    if (e.kind === 'Pod') app.pods.push(e)
    else app.sats.push(e)
  }
  const apps = [...anchors.values()].filter((a) => a.role === 'app')
  const sizeBy = sizeApps(apps, g.metrics, podCount)
  const over = keepBudget(anchors)
  pruneGlobe(anchors, allEdges) // again: the budget may have dropped the globe's targets
  if (over > 0 && filter.groups.has('system')) counters.push(overBudgetCounter(over))
  shapeApps(anchors, filter.groups.has('pods'))
  const links: Link[] = []
  for (const e of allEdges) {
    if (anchors.has(e.from) && anchors.has(e.to)) links.push(linkOf(e))
  }
  links.sort((x, y) => x.id.localeCompare(y.id))
  counters.sort((x, y) => x.id.localeCompare(y.id))
  return { anchors, links, nodeColor: nodeColors(g, palette), counters, statics, sizeBy, focused }
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
    a.collideR = collideClearance(a.entity, 'app', a.r)
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
