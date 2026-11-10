import { useEffect, type RefObject } from 'react'
import { pan, zoomAt, type Camera } from './camera'

// CLICK_SLOP is how far (px) the pointer may move between down and up and still count as a
// click: a drag pans the camera and never selects.
const CLICK_SLOP = 5

// usePanZoom wires wheel zoom (around the cursor), drag-to-pan and click-to-select on the svg.
// A click selects the innermost drawn object under the pointer (a pod before its app) and a
// click on empty space selects nothing. The wheel listener is non-passive so the page itself
// never scrolls.
export function usePanZoom(
  ref: RefObject<SVGSVGElement | null>,
  cam: RefObject<Camera>,
  dirty: RefObject<boolean>,
  onSelect: RefObject<(id: string | null) => void>,
) {
  useEffect(() => {
    const el = ref.current
    if (!el) return
    let drag: { x: number; y: number } | null = null
    let down: { x: number; y: number; target: EventTarget | null } | null = null
    const onWheel = (ev: WheelEvent) => {
      ev.preventDefault()
      const r = el.getBoundingClientRect()
      cam.current = zoomAt(cam.current, Math.exp(-ev.deltaY * 0.0015), ev.clientX - r.left, ev.clientY - r.top)
      dirty.current = true
    }
    const onDown = (ev: PointerEvent) => {
      drag = { x: ev.clientX, y: ev.clientY }
      down = { x: ev.clientX, y: ev.clientY, target: ev.target }
      el.setPointerCapture(ev.pointerId)
      el.classList.add('drag')
    }
    const onMove = (ev: PointerEvent) => {
      if (!drag || !down) return
      if (down.target && Math.hypot(ev.clientX - down.x, ev.clientY - down.y) <= CLICK_SLOP) return
      down.target = null // moved past the slop: this is a pan, not a click
      cam.current = pan(cam.current, ev.clientX - drag.x, ev.clientY - drag.y)
      drag = { x: ev.clientX, y: ev.clientY }
      dirty.current = true
    }
    const onUp = (ev: PointerEvent) => {
      if (ev.type === 'pointerup' && down?.target) onSelect.current(idAt(down.target))
      drag = null
      down = null
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
  }, [ref, cam, dirty, onSelect])
}

// idAt is the data-id of the innermost drawn object containing target, or null.
function idAt(target: EventTarget | null): string | null {
  if (!(target instanceof Element)) return null
  const hit = target.closest('[data-layer="bodies"] [data-id]')
  return hit?.getAttribute('data-id') ?? null
}
