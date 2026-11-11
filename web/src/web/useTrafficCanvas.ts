import { useEffect, useRef, type RefObject } from 'react'
import type { Graph } from '../lib/graph'
import type { Camera } from './camera'
import type { Body } from './layout'
import type { Scene } from './scene'

export type TrafficPainter = (
  ctx: CanvasRenderingContext2D,
  cv: HTMLCanvasElement,
  scene: Scene,
  bodies: Map<string, Body>,
  cam: Camera,
  dpr: number,
  now: number,
) => void

type Options = {
  graph: Graph
  scene: Scene
  bodies: Map<string, Body>
  camRef: RefObject<Camera>
  size: { w: number; h: number }
  visible: boolean
  paint: TrafficPainter
}

function clearCanvas(cv: HTMLCanvasElement | null) {
  const ctx = cv?.getContext('2d')
  if (!cv || !ctx) return
  ctx.setTransform(1, 0, 0, 1, 0, 0)
  ctx.clearRect(0, 0, cv.width, cv.height)
  cv.dataset.flows = '0'
  cv.dataset.drops = '0'
}

// Manages DPR sizing and a sleeping requestAnimationFrame loop for TrafficCanvas.
export function useTrafficCanvas({ graph, scene, bodies, camRef, size, visible, paint }: Options) {
  const cvRef = useRef<HTMLCanvasElement | null>(null)
  const stateRef = useRef({ scene, bodies, size, visible, paint })
  stateRef.current = { scene, bodies, size, visible, paint }

  useEffect(() => {
    const cv = cvRef.current
    if (!cv || size.w <= 0 || size.h <= 0) return
    const dpr = Math.min(2, window.devicePixelRatio || 1)
    cv.width = Math.round(size.w * dpr)
    cv.height = Math.round(size.h * dpr)
  }, [size.w, size.h])

  useEffect(() => {
    let raf = 0
    const tick = () => {
      raf = 0
      const cv = cvRef.current
      const ctx = cv?.getContext('2d')
      const { scene: sc, bodies: bd, size: sz, visible: on, paint: draw } = stateRef.current
      const now = performance.now()
      if (!cv || !ctx || sz.w <= 0 || !on || !graph.flows.active(now)) {
        if (!on) graph.flows.clearInFlight()
        clearCanvas(cv)
        return
      }
      const dpr = Math.min(2, window.devicePixelRatio || 1)
      draw(ctx, cv, sc, bd, camRef.current, dpr, now)
      raf = requestAnimationFrame(tick)
    }
    const wake = () => {
      if (!raf) raf = requestAnimationFrame(tick)
    }
    wake()
    const unsub = graph.flows.subscribe(wake)
    return () => {
      unsub()
      if (raf) cancelAnimationFrame(raf)
    }
  }, [graph, camRef, visible, scene, size.w, size.h])

  return cvRef
}
