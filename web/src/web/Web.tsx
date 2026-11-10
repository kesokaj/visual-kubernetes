import type { Graph } from '../lib/graph'
import type { FilterGroup } from '../lib/wire'
import { Counters } from './Counters'
import { Labels } from './labels'
import { HaloDefs, Travels } from './marks'
import { AnchorView, LinkView, posOf } from './parts'
import { useWeb } from './useWeb'

type Props = {
  graph: Graph
  on: ReadonlySet<FilterGroup>
  fitNonce: number
  selected: string | null
  onSelect: (id: string | null) => void
}

// Web is the spider web: links under bodies, bodies in the zoomed world, labels on top in
// screen space. data-* attributes name what is drawn so tests assert presence and edges,
// never coordinates (ADR-004).
export function Web({ graph, on, fitNonce, selected, onSelect }: Props) {
  const { scene, layout, cam, svgRef } = useWeb(graph, on, fitNonce, onSelect)
  const bodies = layout.bodies
  const now = performance.now()
  const travels = on.has('pods') ? [...graph.motion.travels.values()] : []
  const arriving = new Set(travels.map((t) => t.pod))
  return (
    <>
    <svg
      ref={svgRef}
      className="web"
      data-testid="web"
      data-zoom={cam.k.toFixed(3)}
      data-auto={cam.auto ? 'on' : 'off'}
      data-size-by={scene.sizeBy}
      data-bubbles={scene.anchors.size}
      aria-label="Kubernetes cluster, live view"
      role="img"
    >
      <HaloDefs />
      <g transform={`translate(${cam.x} ${cam.y}) scale(${cam.k})`} data-layer="world">
        <g data-layer="links">
          {scene.links.map((l) => {
            const from = posOf(bodies, l.from)
            const to = posOf(bodies, l.to)
            return from && to ? <LinkView key={l.id} link={l} from={from} to={to} scene={scene} /> : null
          })}
        </g>
        <g data-layer="bodies">
          {[...scene.anchors.values()].map((a) => {
            const at = posOf(bodies, a.id)
            const marks = { pulse: graph.motion.pulses.get(a.id), now, selected, arriving }
            return at ? <AnchorView key={a.id} a={a} at={at} scene={scene} marks={marks} /> : null
          })}
        </g>
        <Travels travels={travels} bodies={bodies} now={now} />
      </g>
      <Labels scene={scene} bodies={bodies} cam={cam} />
    </svg>
    <Counters counters={scene.counters} />
    </>
  )
}
