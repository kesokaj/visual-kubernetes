import { expect, test, type Page } from '@playwright/test'
import { foreignRequests, recordCspViolations, recordRequests, smallText } from './checks'
import { urlOf } from './servers'

// Canvas 2D Hubble traffic particle overlay (plan Phase 6, PRD S3, ADR-003). Forwarded edges
// carry blue particles (#3b82f6); policy-dropped edges carry red particles and red drop marks
// (#dc2626). Toggling the Traffic filter hides and clears the overlay.

const WEB = 'app/vk-demo/Deployment/web'
const API = 'app/vk-demo/Deployment/api'
const CACHE = 'app/vk-demo/Deployment/cache'
const INTERNET = 'ext/internet'

test.use({ baseURL: urlOf('traffic') })

async function sampleEdgePixels(page: Page, srcId: string, dstId: string) {
  return page.evaluate(
    ([sId, dId]) => {
      const cv = document.querySelector<HTMLCanvasElement>('[data-testid="traffic-canvas"]')
      const ctx = cv?.getContext('2d')
      // A body's icon is its sprite <use>, or the inline globe for ext/internet.
      const icon = (id: string) =>
        document.querySelector<SVGGraphicsElement>(`[data-layer="bodies"] > g[data-id="${id}"] > :is(use, g.globe)`)
      const sIcon = icon(sId ?? '')
      const dIcon = icon(dId ?? '')
      if (!cv || !ctx || !sIcon || !dIcon || cv.width === 0 || cv.height === 0) {
        return { blue: 0, red: 0 }
      }
      const cvRect = cv.getBoundingClientRect()
      const sx = cv.width / cvRect.width
      const sy = cv.height / cvRect.height
      const center = (el: SVGGraphicsElement) => {
        const r = el.getBoundingClientRect()
        return { x: (r.left + r.width / 2 - cvRect.left) * sx, y: (r.top + r.height / 2 - cvRect.top) * sy }
      }
      const a = center(sIcon)
      const b = center(dIcon)
      const dx = b.x - a.x
      const dy = b.y - a.y
      const c = { x: (a.x + b.x) / 2 - dy * 0.12, y: (a.y + b.y) / 2 + dx * 0.12 }

      const seen = new Set<number>()
      let blue = 0
      let red = 0
      const pad = Math.ceil(7 * sx)
      for (let i = 0; i <= 40; i++) {
        const t = 0.22 + (0.63 * i) / 40
        const u = 1 - t
        const px = Math.round(u * u * a.x + 2 * u * t * c.x + t * t * b.x)
        const py = Math.round(u * u * a.y + 2 * u * t * c.y + t * t * b.y)
        const x0 = Math.max(0, px - pad)
        const y0 = Math.max(0, py - pad)
        const w = Math.min(cv.width - x0, pad * 2 + 1)
        const h = Math.min(cv.height - y0, pad * 2 + 1)
        if (w <= 0 || h <= 0) continue
        const img = ctx.getImageData(x0, y0, w, h).data
        for (let yy = 0; yy < h; yy++) {
          for (let xx = 0; xx < w; xx++) {
            const key = (y0 + yy) * cv.width + (x0 + xx)
            if (seen.has(key)) continue
            seen.add(key)
            const o = (yy * w + xx) * 4
            const r = img[o]!
            const g = img[o + 1]!
            const bl = img[o + 2]!
            const al = img[o + 3]!
            if (al < 100) continue
            // Forwarded particle #3b82f6 -> (59, 130, 246); guide #cbd5e1 has r >= 203.
            if (bl > 200 && r < 100) blue++
            // Policy-drop particle / mark #dc2626 -> (220, 38, 38); guide #fca5a5 has g >= 165.
            if (r > 190 && g < 80 && bl < 80) red++
          }
        }
      }
      return { blue, red }
    },
    [srcId, dstId],
  )
}

async function nonEmptyCanvasPixels(page: Page): Promise<number> {
  return page.evaluate(() => {
    const cv = document.querySelector<HTMLCanvasElement>('[data-testid="traffic-canvas"]')
    const ctx = cv?.getContext('2d')
    if (!cv || !ctx || cv.width === 0 || cv.height === 0) return 0
    const data = ctx.getImageData(0, 0, cv.width, cv.height).data
    let n = 0
    for (let i = 3; i < data.length; i += 4) {
      if (data[i]! > 0) n++
    }
    return n
  })
}

test('draws forwarded blue particles, red policy-drop particles, and clears when Traffic is off', async ({ page }) => {
  const urls = recordRequests(page)
  const csp = await recordCspViolations(page)
  await page.goto('/')

  const canvas = page.getByTestId('traffic-canvas')
  await expect(canvas).toHaveAttribute('data-flows', '4', { timeout: 10_000 })
  await expect(canvas).toHaveAttribute('data-drops', '1')

  // Forwarded edge (web -> api) renders blue particles and zero red drop pixels.
  await expect
    .poll(async () => (await sampleEdgePixels(page, WEB, API)).blue, { timeout: 5_000 })
    .toBeGreaterThan(0)
  expect((await sampleEdgePixels(page, WEB, API)).red).toBe(0)

  // Policy-dropped edge (web -> cache) renders red drop particles / marks.
  await expect
    .poll(async () => (await sampleEdgePixels(page, WEB, CACHE)).red, { timeout: 5_000 })
    .toBeGreaterThan(0)

  // Toggling Traffic off clears both attributes and every drawn pixel on the canvas.
  await page.locator('.f-pill').click()
  const trafficSwitch = page.getByRole('switch', { name: 'Traffic' })
  await trafficSwitch.click()
  await expect(canvas).toHaveAttribute('data-flows', '0')
  await expect(canvas).toHaveAttribute('data-drops', '0')
  expect(await nonEmptyCanvasPixels(page)).toBe(0)

  // Toggling Traffic back on restores the flows.
  await trafficSwitch.click()
  await expect(canvas).toHaveAttribute('data-flows', '4', { timeout: 5_000 })
  await expect.poll(() => nonEmptyCanvasPixels(page), { timeout: 5_000 }).toBeGreaterThan(0)

  // API hub panel preserves snapshot capabilities across partial hubble-relay updates, and R9/R10 hold.
  await page.getByTestId('web').locator('[data-id="cp/apiserver"]').click({ force: true })
  await expect(page.getByTestId('panel')).toContainText('14 groups')
  await expect(page.getByTestId('panel')).toContainText('hubble-relay')
  await expect(page.getByTestId('panel')).toContainText('relay streaming')
  expect(await smallText(page)).toEqual([])
  expect(foreignRequests(urls, urlOf('traffic'))).toEqual([])
  expect(csp).toEqual([])
})

test('draws the internet globe with world traffic into its entrypoint, hidden with Internet & ingress', async ({ page }) => {
  await page.goto('/')
  const globe = page.locator(`[data-layer="bodies"] > g[data-id="${INTERNET}"]`)
  await expect(globe).toHaveAttribute('data-role', 'ext')
  await expect(globe.locator('[data-icon="globe"]')).toHaveCount(1)
  await expect(page.locator(`[data-label-for="${INTERNET}"]`)).toContainText('1 entrypoint')
  await expect(page.locator(`[data-edge="routes:${INTERNET}->${WEB}"]`)).toHaveCount(1)

  // World flow (ext/internet -> web) carries blue forwarded particles out of the globe.
  await expect
    .poll(async () => (await sampleEdgePixels(page, INTERNET, WEB)).blue, { timeout: 5_000 })
    .toBeGreaterThan(0)

  // The globe opens a panel listing what it reaches, without a config fetch.
  await globe.click({ force: true })
  const panel = page.getByTestId('panel')
  await expect(panel).toHaveAttribute('data-panel-role', 'ext')
  await expect(panel.locator(`[data-entrypoint="${WEB}"]`)).toHaveCount(1)
  await expect(panel.locator('[data-section="config"]')).toHaveCount(0)
  await page.keyboard.press('Escape')

  // Internet & ingress off hides the globe and drops its flow from the overlay.
  await page.locator('.f-pill').click()
  await page.getByRole('switch', { name: 'Internet & ingress' }).click()
  await expect(globe).toHaveCount(0)
  await expect(page.getByTestId('traffic-canvas')).toHaveAttribute('data-flows', '3', { timeout: 5_000 })
})
