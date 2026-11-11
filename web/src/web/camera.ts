// Camera maps world units to screen pixels: screen = world * k + (x, y). While `auto` is on it
// eases toward the fit of everything drawn; a wheel zoom or a drag hands control to the
// presenter until F (or a filter change) turns auto back on.
export type Camera = { x: number; y: number; k: number; auto: boolean }

export type Box = { minX: number; minY: number; maxX: number; maxY: number }

export const ZOOM_MIN = 0.25
export const ZOOM_MAX = 4
const FIT_PAD = 48 // screen pixels kept clear around the fit
const FIT_MAX = 1.35 // a tiny or filtered cluster stays airy instead of blowing up huge
const LABEL_ROOM = 58 // labels sit under bodies, outside their radius

export const initialCamera = (): Camera => ({ x: 0, y: 0, k: 1, auto: true })

function clamp(v: number, lo: number, hi: number): number {
  return Math.min(hi, Math.max(lo, v))
}

// bounds is the box around circles (x, y, r), with room for the labels under them.
export function bounds(circles: Iterable<{ x: number; y: number; r: number }>): Box | null {
  let box: Box | null = null
  for (const c of circles) {
    const b = { minX: c.x - c.r, minY: c.y - c.r, maxX: c.x + c.r, maxY: c.y + c.r + LABEL_ROOM }
    box = box
      ? {
          minX: Math.min(box.minX, b.minX),
          minY: Math.min(box.minY, b.minY),
          maxX: Math.max(box.maxX, b.maxX),
          maxY: Math.max(box.maxY, b.maxY),
        }
      : b
  }
  return box
}

// fit is the camera that shows box inside a w×h screen with FIT_PAD around it.
export function fit(box: Box, w: number, h: number): Camera {
  const bw = Math.max(1, box.maxX - box.minX)
  const bh = Math.max(1, box.maxY - box.minY)
  const k = clamp(Math.min((w - 2 * FIT_PAD) / bw, (h - 2 * FIT_PAD) / bh), ZOOM_MIN, FIT_MAX)
  const cx = (box.minX + box.maxX) / 2
  const cy = (box.minY + box.maxY) / 2
  return { x: w / 2 - cx * k, y: h / 2 - cy * k, k, auto: true }
}

// ease moves cam a fraction t of the way to target (one animation frame of auto-fit).
export function ease(cam: Camera, target: Camera, t: number): Camera {
  return {
    x: cam.x + (target.x - cam.x) * t,
    y: cam.y + (target.y - cam.y) * t,
    k: cam.k + (target.k - cam.k) * t,
    auto: true,
  }
}

// zoomAt scales by factor around the screen point (sx, sy), which stays put.
export function zoomAt(cam: Camera, factor: number, sx: number, sy: number): Camera {
  const k = clamp(cam.k * factor, ZOOM_MIN, ZOOM_MAX)
  const wx = (sx - cam.x) / cam.k
  const wy = (sy - cam.y) / cam.k
  return { x: sx - wx * k, y: sy - wy * k, k, auto: false }
}

export function pan(cam: Camera, dx: number, dy: number): Camera {
  return { ...cam, x: cam.x + dx, y: cam.y + dy, auto: false }
}

export function settled(a: Camera, b: Camera): boolean {
  return Math.abs(a.x - b.x) < 0.5 && Math.abs(a.y - b.y) < 0.5 && Math.abs(a.k - b.k) < 0.001
}
