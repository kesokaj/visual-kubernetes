import { Icon, iconFor } from '../icons/sprite'
import type { Entity } from '../lib/wire'
import type { Body } from './layout'
import { SAT_RADIUS, SIZE, type Anchor, type Link, type Scene } from './scene'

type Pos = { x: number; y: number }

export function posOf(bodies: Map<string, Body>, id: string): Pos | null {
  const b = bodies.get(id)
  return b ? { x: b.x ?? 0, y: b.y ?? 0 } : null
}

// LinkView draws one edge: dashed control-plane links from the API server, node-coloured
// threads from a node to the workloads it hosts, plain lines for the rest.
export function LinkView({ link, from, to, scene }: { link: Link; from: Pos; to: Pos; scene: Scene }) {
  const node = link.kind === 'hosts' ? scene.anchors.get(link.from)?.entity.name : undefined
  const color = node ? scene.nodeColor.get(node) : undefined
  const cls = link.kind === 'member' ? 'cp-link' : link.kind === 'hosts' ? 'thread' : 'edge'
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
  const r = SAT_RADIUS
  return a.sats.map((_, i) => {
    const t = -Math.PI / 4 + (i / Math.max(1, n)) * Math.PI * 2
    return { x: c.x + Math.cos(t) * r, y: c.y + Math.sin(t) * r }
  })
}

function Pod({ pod, at, color }: { pod: Entity; at: Pos; color: string | undefined }) {
  const r = SIZE.pod / 2 + 2
  return (
    <g data-id={pod.id} data-role="pod" data-node={pod.nodeName ?? ''} data-status={pod.status}>
      <circle
        className={color ? 'pod-ring' : 'pod-ring pending'}
        cx={at.x}
        cy={at.y}
        r={r}
        stroke={color ?? '#94a3b8'}
      />
      <Icon name="pod" size={SIZE.pod} x={at.x} y={at.y} />
    </g>
  )
}

// AnchorView draws one body: its icon, and for an app its satellites and pod ring.
export function AnchorView({ a, at, scene }: { a: Anchor; at: Pos; scene: Scene }) {
  const sys = a.entity.filterGroup === 'system'
  return (
    <g
      data-id={a.id}
      data-role={a.role}
      data-kind={a.entity.kind}
      data-pods={a.role === 'app' ? a.pods.length : undefined}
      className={sys ? 'sys' : undefined}
    >
      {a.role === 'app' && a.ring > 0 && <circle className="ring" cx={at.x} cy={at.y} r={a.ring} />}
      {a.role === 'counter' ? (
        <circle className="counter" cx={at.x} cy={at.y} r={SIZE.counter / 2} />
      ) : (
        <Icon name={iconFor(a.entity.kind, a.role === 'app')} size={a.icon} x={at.x} y={at.y} />
      )}
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
          <Pod key={pod.id} pod={pod} at={p} color={pod.nodeName ? scene.nodeColor.get(pod.nodeName) : undefined} />
        ) : null
      })}
    </g>
  )
}

function NodeDot({ a, at, scene }: { a: Anchor; at: Pos; scene: Scene }) {
  const color = scene.nodeColor.get(a.entity.name)
  return <circle cx={at.x + a.icon / 2 - 4} cy={at.y - a.icon / 2 + 4} r={7} fill={color} stroke="#fff" strokeWidth={2} />
}
