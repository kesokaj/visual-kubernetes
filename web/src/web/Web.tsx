import type { FilterState } from '../filter/state'
import type { Graph } from '../lib/graph'
import { Caption } from '../replay/Caption'
import type { Play } from '../replay/player'
import { Counters } from './Counters'
import { Labels } from './labels'
import { GridPaper, HaloDefs, Travels } from './marks'
import { AnchorView, LinkView, posOf } from './parts'
import { TrafficCanvas } from './TrafficCanvas'
import { useWeb } from './useWeb'

type Props = {
  graph: Graph
  filter: FilterState
  fitNonce: number
  selected: string | null
  onSelect: (id: string | null) => void
  play: Play
}

// Web is the spider web: subtle drafting grid paper on white, links under bodies, bodies in the
// zoomed world, labels on top in screen space, traffic particles on the 2D canvas, and the replay
// caption beside the body its step is about.
export function Web({ graph, filter, fitNonce, selected, onSelect, play }: Props) {
  const { scene, layout, cam, camRef, size, svgRef } = useWeb(graph, filter, fitNonce, onSelect)
  const bodies = layout.bodies
  const now = performance.now()
  const travels = filter.groups.has('pods') ? [...graph.motion.travels.values()] : []
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
        <HaloDefs cam={cam} />
        <GridPaper />
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
      <TrafficCanvas
        graph={graph}
        scene={scene}
        bodies={bodies}
        camRef={camRef}
        size={size}
        visible={filter.groups.has('traffic')}
      />
      <Counters counters={scene.counters} />
      <Caption play={play} graph={graph} scene={scene} bodies={bodies} cam={cam} size={size} />
    </>
  )
}
