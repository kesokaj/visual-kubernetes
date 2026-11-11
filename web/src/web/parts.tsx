import { Icon, iconFor } from '../icons/sprite'
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
// threads from a node to the workloads it hosts, plain lines for the rest.
export function LinkView({ link, from, to, scene }: { link: Link; from: Pos; to: Pos; scene: Scene }) {
  const fromAnchor = scene.anchors.get(link.from)
  const toAnchor = scene.anchors.get(link.to)
  const node = link.kind === 'hosts' ? fromAnchor?.entity.name : undefined
  const color = node ? scene.nodeColor.get(node) : undefined
  const base = link.kind === 'member' ? 'cp-link' : link.kind === 'hosts' ? 'thread' : 'edge'
  const cls = fromAnchor?.dimmed || toAnchor?.dimmed ? `${base} dimmed` : base
  return (
    <line
      className={cls}
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
function satPositions(a: Anchor, c: Pos): Pos[] {
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

export type Marks = { pulse: Pulse | undefined; now: number; selected: string | null; arriving: ReadonlySet<string> }

// AnchorView draws one body: its icon, and for an app its halo, pulse, satellites and pod ring.
export function AnchorView({ a, at, scene, marks }: { a: Anchor; at: Pos; scene: Scene; marks: Marks }) {
  const sys = a.entity.filterGroup === 'system'
  const app = a.role === 'app'
  const statics = scene.statics.get(a.id)
  const cls = a.dimmed ? 'dimmed' : !scene.focused && sys ? 'sys' : undefined
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
      className={cls}
    >
      {a.top > 0 && <Halo at={at} r={a.icon * 1.05} />}
      {app && a.ring > 0 && <circle className="ring" cx={at.x} cy={at.y} r={a.ring} />}
      {app && <PulseRing pulse={marks.pulse} now={marks.now} at={at} r={a.icon / 2 + 6} />}
      {marks.selected === a.id && <circle className="sel-ring" cx={at.x} cy={at.y} r={a.icon / 2 + 8} />}
      <Icon name={iconFor(a.entity.kind, app)} size={a.icon} x={at.x} y={at.y} />
      {a.role === 'node' && <NodeDot a={a} at={at} scene={scene} />}
      {satPositions(a, at).map((p, i) => {
        const s = a.sats[i]
        return s ? (
          <g key={s.id} data-id={s.id} data-role="sat" data-kind={s.kind}>
            <Icon name={iconFor(s.kind, false)} size={SIZE.sat} x={p.x} y={p.y} />
          </g>
        ) : null
      })}
      {podPositions(a, at).map((p, i) => {
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
