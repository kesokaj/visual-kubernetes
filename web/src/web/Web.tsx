import type { Graph } from '../lib/graph'
import type { FilterGroup } from '../lib/wire'
import { Labels } from './labels'
import { AnchorView, LinkView, posOf } from './parts'
import { useWeb } from './useWeb'

// Web is the spider web: links under bodies, bodies in the zoomed world, labels on top in
// screen space. data-* attributes name what is drawn so tests assert presence and edges,
// never coordinates (ADR-004).
export function Web({ graph, on, fitNonce }: { graph: Graph; on: ReadonlySet<FilterGroup>; fitNonce: number }) {
  const { scene, layout, cam, svgRef } = useWeb(graph, on, fitNonce)
  const bodies = layout.bodies
  return (
    <svg
      ref={svgRef}
      className="web"
      data-testid="web"
      data-zoom={cam.k.toFixed(3)}
      data-auto={cam.auto ? 'on' : 'off'}
      aria-label="Kubernetes cluster, live view"
      role="img"
    >
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
            return at ? <AnchorView key={a.id} a={a} at={at} scene={scene} /> : null
          })}
        </g>
      </g>
      <Labels scene={scene} bodies={bodies} cam={cam} />
    </svg>
  )
}
