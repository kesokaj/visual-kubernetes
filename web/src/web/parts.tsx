import { Globe, Icon, iconFor } from '../icons/sprite'
import type { Pulse } from '../lib/motion'
import type { Entity } from '../lib/wire'
import type { Body } from './layout'
import { Halo, PulseRing } from './marks'
import { SIZE, type Anchor, type Link, type Scene } from './scene'

export type Pos = { x: number; y: number }

export function posOf(bodies: Map<string, Body>, id: string): Pos | null {
  const b = bodies.get(id)
  return b ? { x: b.x ?? 0, y: b.y ?? 0 } : null
}

// LinkView draws one edge: dashed control-plane links from the API server, node-coloured
// threads from a node to the workloads it hosts, plain lines for the rest. faded is the
// neighbourhood fade (neighborhood.ts), applied on top of a D54 dim.
type LinkProps = { link: Link; from: Pos; to: Pos; scene: Scene; faded: boolean }

export function LinkView({ link, from, to, scene, faded }: LinkProps) {
  const fromAnchor = scene.anchors.get(link.from)
  const toAnchor = scene.anchors.get(link.to)
  const node = link.kind === 'hosts' ? fromAnchor?.entity.name : undefined
  const color = node ? scene.nodeColor.get(node) : undefined
  const base = link.kind === 'member' ? 'cp-link' : link.kind === 'hosts' ? 'thread' : 'edge'
  const dim = fromAnchor?.dimmed || toAnchor?.dimmed ? `${base} dimmed` : base
  const cls = faded ? `${dim} faded` : dim
  return (
    <line
      className={cls}
      data-faded={faded ? 'true' : undefined}
      data-edge={link.id}
      data-kind={link.kind}
      x1={from.x}
      y1={from.y}
      x2={to.x}
      y2={to.y}
      stroke={color}
    />
  )
}

// podPositions spreads an app's pods evenly on its ring, starting at the top.
export function podPositions(a: Anchor, c: Pos): Pos[] {
  const n = a.pods.length
  return a.pods.map((_, i) => {
    const t = -Math.PI / 2 + (i / Math.max(1, n)) * Math.PI * 2
    return { x: c.x + Math.cos(t) * a.ring, y: c.y + Math.sin(t) * a.ring }
  })
}

// satPositions puts app-owned satellites (Service, HPA, ConfigMap…) between the icon and the
// pod ring.
export function satPositions(a: Anchor, c: Pos): Pos[] {
  const n = a.sats.length
  return a.sats.map((_, i) => {
    const t = -Math.PI / 4 + (i / Math.max(1, n)) * Math.PI * 2
    return { x: c.x + Math.cos(t) * a.satR, y: c.y + Math.sin(t) * a.satR }
  })
}

type PodProps = { pod: Entity; at: Pos; color: string | undefined; arriving: boolean; selected: boolean }

function Pod({ pod, at, color, arriving, selected }: PodProps) {
  const r = SIZE.pod / 2 + 2
  const cls = ['pod-ring', color ? '' : 'pending', selected ? 'sel' : ''].filter(Boolean).join(' ')
  return (
    <g
      data-id={pod.id}
      data-role="pod"
      data-node={pod.nodeName ?? ''}
      data-status={pod.status}
      className={arriving ? 'arriving' : undefined}
    >
      <circle className={cls} cx={at.x} cy={at.y} r={r} stroke={color ?? '#94a3b8'} />
      <Icon name="pod" size={SIZE.pod} x={at.x} y={at.y} />
    </g>
  )
}

export type Marks = {
  pulse: Pulse | undefined
  now: number
  selected: string | null
  arriving: ReadonlySet<string>
  faded: boolean // outside the hovered or selected neighbourhood
}

// groupRadius is the faint disc that holds an app with its riders: past the pod ring when pods
// are drawn, else past the satellite orbit; 0 when the app has no riders.
function groupRadius(a: Anchor): number {
  if (a.ring > 0 && a.pods.length > 0) return a.ring + SIZE.pod / 2 + 6
  return a.sats.length > 0 ? a.satR + SIZE.sat / 2 + 6 : 0
}

// tetherPath is one path with a spoke from the app centre to each of its pods and satellites,
// so riders read as part of their workload rather than loose dots (plan D1).
function tetherPath(c: Pos, riders: Pos[]): string {
  return riders.map((p) => `M${c.x.toFixed(1)} ${c.y.toFixed(1)}L${p.x.toFixed(1)} ${p.y.toFixed(1)}`).join('')
}

function Group({ a, at, sats, pods }: { a: Anchor; at: Pos; sats: Pos[]; pods: Pos[] }) {
  const r = groupRadius(a)
  if (r === 0) return null
  return (
    <>
      <circle className="group-disc" cx={at.x} cy={at.y} r={r} />
      <path className="tether" d={tetherPath(at, [...pods, ...sats])} data-spokes={pods.length + sats.length} />
    </>
  )
}

// AnchorView draws one body: its icon, and for an app its halo, pulse, satellites and pod ring.
export function AnchorView({ a, at, scene, marks }: { a: Anchor; at: Pos; scene: Scene; marks: Marks }) {
  const sys = a.entity.filterGroup === 'system'
  const app = a.role === 'app'
  const statics = scene.statics.get(a.id)
  const base = a.dimmed ? 'dimmed' : !scene.focused && sys ? 'sys' : undefined
  const cls = marks.faded ? [base, 'faded'].filter(Boolean).join(' ') : base
  const sats = satPositions(a, at)
  const pods = podPositions(a, at)
  return (
    <g
      data-id={a.id}
      data-role={a.role}
      data-kind={a.entity.kind}
      data-pods={app ? a.pods.length : undefined}
      data-size={app ? a.icon : undefined}
      data-measure={app ? a.measure : undefined}
      data-top={app && a.top > 0 ? a.top : undefined}
      data-pulses={app ? (marks.pulse?.n ?? 0) : undefined}
      data-statics={statics ? statics.length : undefined}
      data-selected={marks.selected === a.id ? 'true' : undefined}
      data-dimmed={a.dimmed ? 'true' : undefined}
      data-faded={marks.faded ? 'true' : undefined}
      className={cls}
    >
      {app && <Group a={a} at={at} sats={sats} pods={pods} />}
      {a.top > 0 && <Halo at={at} r={a.icon * 1.05} />}
      {app && a.ring > 0 && <circle className="ring" cx={at.x} cy={at.y} r={a.ring} />}
      {app && <PulseRing pulse={marks.pulse} now={marks.now} at={at} r={a.icon / 2 + 6} />}
      {marks.selected === a.id && <circle className="sel-ring" cx={at.x} cy={at.y} r={a.icon / 2 + 8} />}
      {a.role === 'ext' ? (
        <Globe size={a.icon} x={at.x} y={at.y} />
      ) : (
        <Icon name={iconFor(a.entity.kind, app)} size={a.icon} x={at.x} y={at.y} />
      )}
      {a.role === 'node' && <NodeDot a={a} at={at} scene={scene} />}
      {sats.map((p, i) => {
        const s = a.sats[i]
        return s ? (
          <g key={s.id} data-id={s.id} data-role="sat" data-kind={s.kind}>
            <Icon name={iconFor(s.kind, false)} size={SIZE.sat} x={p.x} y={p.y} />
          </g>
        ) : null
      })}
      {pods.map((p, i) => {
        const pod = a.pods[i]
        return pod ? (
          <Pod
            key={pod.id}
            pod={pod}
            at={p}
            color={pod.nodeName ? scene.nodeColor.get(pod.nodeName) : undefined}
            arriving={marks.arriving.has(pod.id)}
            selected={marks.selected === pod.id}
          />
        ) : null
      })}
    </g>
  )
}

function NodeDot({ a, at, scene }: { a: Anchor; at: Pos; scene: Scene }) {
  const color = scene.nodeColor.get(a.entity.name)
  return <circle cx={at.x + a.icon / 2 - 4} cy={at.y - a.icon / 2 + 4} r={7} fill={color} stroke="#fff" strokeWidth={2} />
}
