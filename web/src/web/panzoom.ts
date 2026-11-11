import { useEffect, type RefObject } from 'react'
import { pan, toWorld, zoomAt, zoomTo, ZOOM_MAX, type Camera } from './camera'
import type { Layout } from './layout'

// CLICK_SLOP is how far (px) the pointer may move between down and up and still count as a
// click: past it a press on a body drags the body, a press on empty space pans, and neither
// selects.
const CLICK_SLOP = 5
const HUB = 'cp/apiserver' // pinned at the centre: pressing it pans instead of dragging
const DBL_ZOOM = 1.6 // double-click zoom step

// PanZoom is what the pointer handlers read and write. It must be one stable object for the
// life of the web (useWeb keeps it in a ref), so the listeners attach once.
export type PanZoom = {
  cam: RefObject<Camera>
  target: RefObject<Camera | null> // a presenter zoom the frame loop glides toward
  dirty: RefObject<boolean>
  layout: Layout
  size: RefObject<{ w: number; h: number }>
  onSelect: RefObject<(id: string | null) => void>
  onHover: RefObject<(id: string | null) => void>
}

type Press = { x: number; y: number; target: EventTarget | null; moved: boolean; last: { x: number; y: number } }
type Grab = { id: string; dx: number; dy: number }

// usePanZoom wires the pointer on the svg: wheel and pinch zoom around the cursor, drag a body
// to move it while the web reflows around it (plan D7), drag empty space to pan, double-click to
// zoom in, hover to light a neighbourhood, and click to select the innermost object (a pod
// before its app; empty space selects nothing). The wheel listener is non-passive so the page
// itself never scrolls.
export function usePanZoom(ref: RefObject<SVGSVGElement | null>, pz: PanZoom) {
  useEffect(() => {
    const el = ref.current
    if (!el) return
    const h = handlers(el, pz)
    const on = Object.entries(h) as [keyof typeof h, EventListener][]
    for (const [type, fn] of on) el.addEventListener(type, fn, type === 'wheel' ? { passive: false } : undefined)
    return () => {
      for (const [type, fn] of on) el.removeEventListener(type, fn)
    }
  }, [ref, pz])
}

function handlers(el: SVGSVGElement, pz: PanZoom) {
  let press: Press | null = null
  let grab: Grab | null = null
  const local = (ev: MouseEvent) => {
    const r = el.getBoundingClientRect()
    return { x: ev.clientX - r.left, y: ev.clientY - r.top }
  }
  const presenter = (cam: Camera) => {
    pz.cam.current = cam
    pz.target.current = null
    pz.dirty.current = true
  }
  const end = () => {
    if (grab) pz.layout.release(grab.id)
    press = null
    grab = null
    el.classList.remove('drag')
  }
  return {
    wheel: (ev: WheelEvent) => {
      ev.preventDefault()
      const p = local(ev)
      // A pinch arrives as a ctrl-wheel with small deltas: scale it up so it feels direct.
      const per = ev.ctrlKey ? 0.01 : 0.0015
      const dy = ev.deltaMode === 1 ? ev.deltaY * 16 : ev.deltaY
      presenter(zoomAt(pz.cam.current, Math.exp(-dy * per), p.x, p.y))
    },
    pointerdown: (ev: PointerEvent) => {
      const p = local(ev)
      press = { x: ev.clientX, y: ev.clientY, target: ev.target, moved: false, last: { x: ev.clientX, y: ev.clientY } }
      grab = grabAt(ev.target, pz, p.x, p.y)
      el.setPointerCapture(ev.pointerId)
      el.classList.add('drag')
    },
    pointermove: (ev: PointerEvent) => {
      if (!press) {
        pz.onHover.current(idAt(ev.target))
        return
      }
      if (!press.moved && Math.hypot(ev.clientX - press.x, ev.clientY - press.y) <= CLICK_SLOP) return
      press.moved = true
      if (grab) {
        const p = local(ev)
        const w = toWorld(pz.cam.current, p.x, p.y)
        pz.layout.drag(grab.id, w.x - grab.dx, w.y - grab.dy)
        presenter({ ...pz.cam.current, auto: false })
      } else {
        presenter(pan(pz.cam.current, ev.clientX - press.last.x, ev.clientY - press.last.y))
      }
      press.last = { x: ev.clientX, y: ev.clientY }
    },
    pointerup: () => {
      if (press && !press.moved) pz.onSelect.current(idAt(press.target))
      end()
    },
    pointercancel: () => end(),
    pointerleave: () => pz.onHover.current(null),
    dblclick: (ev: MouseEvent) => {
      const p = local(ev)
      const cam = pz.target.current ?? pz.cam.current
      const id = anchorAt(ev.target)
      const b = id ? pz.layout.bodies.get(id) : undefined
      const { w, h } = pz.size.current
      const next =
        b && w > 0
          ? zoomTo(b.x ?? 0, b.y ?? 0, Math.min(ZOOM_MAX, Math.max(cam.k * 2, DBL_ZOOM)), w, h)
          : zoomAt(cam, DBL_ZOOM, p.x, p.y)
      pz.cam.current = { ...pz.cam.current, auto: false }
      pz.target.current = next
      pz.dirty.current = true
    },
  }
}

// grabAt is the body a press would drag, with the press's offset from the body centre in world
// units so the body does not jump under the pointer; null on empty space or on the hub.
function grabAt(target: EventTarget | null, pz: PanZoom, sx: number, sy: number): Grab | null {
  const id = anchorAt(target)
  const b = id && id !== HUB ? pz.layout.bodies.get(id) : undefined
  if (!id || !b) return null
  const w = toWorld(pz.cam.current, sx, sy)
  return { id, dx: w.x - (b.x ?? 0), dy: w.y - (b.y ?? 0) }
}

// idAt is the data-id of the innermost drawn object containing target, or null.
function idAt(target: EventTarget | null): string | null {
  if (!(target instanceof Element)) return null
  return target.closest('[data-layer="bodies"] [data-id]')?.getAttribute('data-id') ?? null
}

// anchorAt is the data-id of the body (an anchor, not a pod on its ring) containing target.
function anchorAt(target: EventTarget | null): string | null {
  if (!(target instanceof Element)) return null
  return target.closest('[data-layer="bodies"] > [data-id]')?.getAttribute('data-id') ?? null
}
