import type { Camera } from './camera'
import type { Body } from './layout'
import { SIZE, type Anchor, type Scene } from './scene'

// Labels are drawn in screen space, outside the zoomed world, so their size never depends on
// the zoom: names are 14px and status lines 12px at any scale (PRD R10).
const NAME_GAP = 18 // px below the body to the name baseline (a 14px name's ascent plus clearance)
const POD_RING_OUT = SIZE.pod / 2 + 4 // pod ring radius (parts.tsx: pod/2 + 2) plus its stroke
const SUB_GAP = 15 // px from the name baseline to the status baseline
const SUB_MIN_ZOOM = 0.45 // below this zoom status lines are dropped to keep the web readable

function labelText(a: Anchor): { name: string; sub: string } {
  const e = a.entity
  switch (a.role) {
    case 'app':
      return { name: e.name, sub: [e.namespace, e.status].filter(Boolean).join(' · ') }
    case 'sat':
      return { name: e.name, sub: e.kind }
    default:
      return { name: e.name, sub: e.status }
  }
}

// bodyBottom is how far below its centre a body's drawing reaches, in world units.
function bodyBottom(a: Anchor): number {
  if (a.role !== 'app') return a.icon / 2
  return a.ring > 0 ? a.ring + POD_RING_OUT : a.sats.length > 0 ? a.satR + SIZE.sat / 2 : a.icon / 2
}

export function Labels({ scene, bodies, cam }: { scene: Scene; bodies: Map<string, Body>; cam: Camera }) {
  const showSub = cam.k >= SUB_MIN_ZOOM
  return (
    <g data-layer="labels">
      {[...scene.anchors.values()].map((a) => {
        const b = bodies.get(a.id)
        if (!b) return null
        const { name, sub } = labelText(a)
        const x = (b.x ?? 0) * cam.k + cam.x
        const y = ((b.y ?? 0) + bodyBottom(a)) * cam.k + cam.y + NAME_GAP
        const cls = a.dimmed ? 'dimmed' : !scene.focused && a.entity.filterGroup === 'system' ? 'sys' : undefined
        return (
          <g key={a.id} data-label-for={a.id} className={cls}>
            <text className="lbl name" x={x} y={y} textAnchor="middle">
              {name}
            </text>
            {showSub && sub && (
              <text className="lbl sub" x={x} y={y + SUB_GAP} textAnchor="middle">
                {sub}
              </text>
            )}
          </g>
        )
      })}
    </g>
  )
}
