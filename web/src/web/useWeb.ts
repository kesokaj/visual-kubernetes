import { useEffect, useMemo, useReducer, useRef, useState, useSyncExternalStore } from 'react'
import type { RefObject } from 'react'
import type { Graph } from '../lib/graph'
import type { FilterGroup } from '../lib/wire'
import { bounds, ease, fit, initialCamera, pan, settled, zoomAt, type Camera } from './camera'
import { Layout } from './layout'
import { buildScene, type Scene } from './scene'

export type WebState = {
  scene: Scene
  layout: Layout
  cam: Camera
  size: { w: number; h: number }
  svgRef: RefObject<SVGSVGElement | null>
}

// useWeb is the controller of the force web: it rebuilds the scene from the graph and the
// filter, feeds the layout, runs the camera and re-renders once per animation frame while
// anything moves. fitNonce changes when the presenter presses F or a filter changes.
export function useWeb(graph: Graph, on: ReadonlySet<FilterGroup>, fitNonce: number): WebState {
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
  const size = useSize(svgRef)

  // version is the graph's change counter: the scene reads the graph, which mutates in place.
  const scene = useMemo(() => buildScene(graph, on, palette), [graph, on, palette, version])

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
      if (cam.current.auto && size.w > 0) {
        const box = bounds(circles(l))
        if (box) {
          const target = fit(box, size.w, size.h)
          if (!settled(cam.current, target)) {
            cam.current = ease(cam.current, target, 0.12)
            dirty.current = true
          }
        }
      }
      if (dirty.current) {
        dirty.current = false
        frame()
      }
    }
    raf = requestAnimationFrame(loop)
    return () => cancelAnimationFrame(raf)
  }, [size.w, size.h])

  usePanZoom(svgRef, cam, dirty)

  return { scene, layout: layout.current, cam: cam.current, size, svgRef }
}

// circles are the bodies as plain circles for the camera fit.
function circles(l: Layout) {
  return [...l.bodies.values()].map((b) => ({ x: b.x ?? 0, y: b.y ?? 0, r: b.r }))
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

// usePanZoom wires wheel zoom (around the cursor) and drag-to-pan on the svg. The wheel
// listener is non-passive so the page itself never scrolls.
function usePanZoom(
  ref: RefObject<SVGSVGElement | null>,
  cam: RefObject<Camera>,
  dirty: RefObject<boolean>,
) {
  useEffect(() => {
    const el = ref.current
    if (!el) return
    let drag: { x: number; y: number } | null = null
    const onWheel = (ev: WheelEvent) => {
      ev.preventDefault()
      const r = el.getBoundingClientRect()
      cam.current = zoomAt(cam.current, Math.exp(-ev.deltaY * 0.0015), ev.clientX - r.left, ev.clientY - r.top)
      dirty.current = true
    }
    const onDown = (ev: PointerEvent) => {
      drag = { x: ev.clientX, y: ev.clientY }
      el.setPointerCapture(ev.pointerId)
      el.classList.add('drag')
    }
    const onMove = (ev: PointerEvent) => {
      if (!drag) return
      cam.current = pan(cam.current, ev.clientX - drag.x, ev.clientY - drag.y)
      drag = { x: ev.clientX, y: ev.clientY }
      dirty.current = true
    }
    const onUp = () => {
      drag = null
      el.classList.remove('drag')
    }
    el.addEventListener('wheel', onWheel, { passive: false })
    el.addEventListener('pointerdown', onDown)
    el.addEventListener('pointermove', onMove)
    el.addEventListener('pointerup', onUp)
    el.addEventListener('pointercancel', onUp)
    return () => {
      el.removeEventListener('wheel', onWheel)
      el.removeEventListener('pointerdown', onDown)
      el.removeEventListener('pointermove', onMove)
      el.removeEventListener('pointerup', onUp)
      el.removeEventListener('pointercancel', onUp)
    }
  }, [ref, cam, dirty])
}
