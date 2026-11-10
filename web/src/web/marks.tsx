import { Icon } from '../icons/sprite'
import { PULSE_MS, TRAVEL_MS, type Pulse, type Travel } from '../lib/motion'
import type { Body } from './layout'
import { SIZE } from './scene'

type Pos = { x: number; y: number }

// HaloDefs holds the gradient the top-3 halo paints with (design/mockup-v3.html: #f5b83d).
export function HaloDefs() {
  return (
    <defs>
      <radialGradient id="vk-halo">
        <stop offset="0%" stopColor="#f5b83d" stopOpacity={0.45} />
        <stop offset="60%" stopColor="#f5b83d" stopOpacity={0.18} />
        <stop offset="100%" stopColor="#f5b83d" stopOpacity={0} />
      </radialGradient>
    </defs>
  )
}

export function Halo({ at, r }: { at: Pos; r: number }) {
  return <circle className="halo" cx={at.x} cy={at.y} r={r} fill="url(#vk-halo)" />
}

// PulseRing is one expanding ring after a scale, restart or new pod. Keyed by the pulse count
// so a second pulse restarts the CSS animation instead of extending the first.
export function PulseRing({ pulse, now, at, r }: { pulse: Pulse | undefined; now: number; at: Pos; r: number }) {
  if (!pulse || now - pulse.at >= PULSE_MS) return null
  return <circle key={pulse.n} className="pulse" data-pulse={pulse.n} cx={at.x} cy={at.y} r={r} />
}

// Travels draws each pod newly placed on a node as a pod icon moving from the node to its app
// along their thread; the pod on the app's ring fades in as it arrives.
export function Travels({ travels, bodies, now }: { travels: Iterable<Travel>; bodies: Map<string, Body>; now: number }) {
  const out = []
  for (const t of travels) {
    const from = bodies.get(t.node)
    const to = bodies.get(t.app)
    if (!from || !to) continue
    const k = ease(Math.min(1, Math.max(0, (now - t.at) / TRAVEL_MS)))
    const x = (from.x ?? 0) + ((to.x ?? 0) - (from.x ?? 0)) * k
    const y = (from.y ?? 0) + ((to.y ?? 0) - (from.y ?? 0)) * k
    out.push(
      <g key={t.pod} className="travel" data-travel={t.pod} data-node={t.node} data-app={t.app}>
        <Icon name="pod" size={SIZE.pod} x={x} y={y} />
      </g>,
    )
  }
  return <g data-layer="travels">{out}</g>
}

function ease(t: number): number {
  return t < 0.5 ? 2 * t * t : 1 - (-2 * t + 2) ** 2 / 2
}
