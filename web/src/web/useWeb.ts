import { useEffect, useMemo, useReducer, useRef, useState, useSyncExternalStore } from 'react'
import type { RefObject } from 'react'
import type { FilterState } from '../filter/state'
import type { Graph } from '../lib/graph'
import { bounds, ease, fit, initialCamera, settled, type Camera } from './camera'
import { Layout } from './layout'
import { usePanZoom } from './panzoom'
import { buildScene, type Scene } from './scene'

export type WebState = {
  scene: Scene
  layout: Layout
  cam: Camera
  camRef: RefObject<Camera>
  size: { w: number; h: number }
  svgRef: RefObject<SVGSVGElement | null>
}

// useWeb is the controller of the force web: it rebuilds the scene from the graph and the
// filter, feeds the layout, runs the camera and re-renders once per animation frame while
// anything moves (bodies, the camera, a pulse or a pod travelling). fitNonce changes when the
// presenter presses F or a structural filter changes; onSelect gets each click's object id.
export function useWeb(
  graph: Graph,
  filter: FilterState,
  fitNonce: number,
  onSelect: (id: string | null) => void,
): WebState {
  const version = useSyncExternalStore(graph.subscribe, graph.getVersion, graph.getVersion)
  const palette = useRef(new Map<string, string>()).current
  const dirty = useRef(true)
  const [, frame] = useReducer((n: number) => n + 1, 0)
  const layout = useRef<Layout | null>(null)
  layout.current ??= new Layout(() => {
    dirty.current = true
  })
  const cam = useRef(initialCamera())
  const svgRef = useRef<SVGSVGElement | null>(null)
  const select = useRef(onSelect)
  select.current = onSelect
  const size = useSize(svgRef)

  // version is the graph's change counter: the scene reads the graph, which mutates in place.
  const scene = useMemo(() => buildScene(graph, filter, palette), [graph, filter, palette, version])

  useEffect(() => {
    layout.current?.update(scene)
    dirty.current = true
  }, [scene])

  useEffect(() => {
    cam.current = { ...cam.current, auto: true }
    dirty.current = true
  }, [fitNonce])

  useEffect(() => () => layout.current?.stop(), [])

  useEffect(() => {
    let raf = 0
    const loop = () => {
      raf = requestAnimationFrame(loop)
      const l = layout.current
      if (!l) return
      if (cam.current.auto && size.w > 0) follow(cam, l, size, dirty)
      if (graph.motion.active(performance.now())) dirty.current = true
      if (dirty.current) {
        dirty.current = false
        frame()
      }
    }
    raf = requestAnimationFrame(loop)
    return () => cancelAnimationFrame(raf)
  }, [graph, size])

  usePanZoom(svgRef, cam, dirty, select)

  return { scene, layout: layout.current, cam: cam.current, camRef: cam, size, svgRef }
}

// follow eases the auto camera one frame toward the fit of every body.
function follow(cam: RefObject<Camera>, l: Layout, size: { w: number; h: number }, dirty: RefObject<boolean>) {
  const box = bounds([...l.bodies.values()].map((b) => ({ x: b.x ?? 0, y: b.y ?? 0, r: b.r })))
  if (!box) return
  const target = fit(box, size.w, size.h)
  if (settled(cam.current, target)) return
  cam.current = ease(cam.current, target, 0.12)
  dirty.current = true
}

function useSize(ref: RefObject<SVGSVGElement | null>) {
  const [size, setSize] = useState({ w: 0, h: 0 })
  useEffect(() => {
    const el = ref.current
    if (!el) return
    const ro = new ResizeObserver(() => setSize({ w: el.clientWidth, h: el.clientHeight }))
    ro.observe(el)
    setSize({ w: el.clientWidth, h: el.clientHeight })
    return () => ro.disconnect()
  }, [ref])
  return size
}
