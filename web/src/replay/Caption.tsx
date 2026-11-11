import type { Graph } from '../lib/graph'
import type { Camera } from '../web/camera'
import type { Body } from '../web/layout'
import type { Scene } from '../web/scene'
import type { Play } from './player'
import { clockOf, objectLabel, targetOf } from './target'

type Props = {
  play: Play
  graph: Graph
  scene: Scene
  bodies: Map<string, Body>
  cam: Camera
  size: { w: number; h: number }
}

const LIFT = 34 // px above the target body's centre to the caption's pointer
const HALF = 170 // half the caption's max width (replay.css .chain-caption), keeps it on screen

// Caption narrates one step of a replayed causal chain beside the body it is about (plan D17).
// It is HTML in screen space, outside the zoomed world, so its text stays 12–16px at any zoom
// (PRD R10). data-* attributes name the step for tests; no button, the keys drive it (D7).
export function Caption({ play, graph, scene, bodies, cam, size }: Props) {
  if (!play) return null
  const step = play.chain.steps[play.index]
  if (!step) return null
  const target = targetOf(step, graph, scene)
  const b = bodies.get(target)
  const x = b ? (b.x ?? 0) * cam.k + cam.x : size.w / 2
  const y = b ? (b.y ?? 0) * cam.k + cam.y - LIFT : 96
  const left = Math.min(Math.max(x, HALF + 8), Math.max(size.w - HALF - 8, HALF + 8))
  const top = Math.max(Math.min(y, size.h - 8), 96)
  const total = play.chain.steps.length
  return (
    <div
      className="chain-caption"
      data-testid="chain-caption"
      data-chain-id={play.chain.id}
      data-step={play.index + 1}
      data-steps={total}
      data-verb={step.verb}
      data-target={target}
      data-paused={play.paused ? 'true' : 'false'}
      style={{ left, top }}
      role="status"
    >
      <div className="cc-meta">
        step {play.index + 1} of {total} · {clockOf(step.at)}
        {play.paused && ' · paused'}
      </div>
      <div className="cc-verb">
        {step.verb}
        {step.derived && <span className="cc-tag">inferred</span>}
      </div>
      <div className="cc-obj">{objectLabel(step.object)}</div>
      <div className="cc-actor">by {step.actor || 'unknown'}</div>
    </div>
  )
}
