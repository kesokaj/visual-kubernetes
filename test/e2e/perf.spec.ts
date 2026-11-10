import { expect, test } from '@playwright/test'
import { urlOf } from './servers'

// Perf probe (plan Phase 4, PRD G3/S5): on the large-cluster stream, at 1600×900 in headless
// Chromium, the median frame time over 10 s stays at or under 33.3 ms (≥ 30 fps). Every frame
// is made to re-render: a wheel event each frame zooms the camera in and out, so the probe
// measures drawing the whole web, not an idle page.

test.use({ baseURL: urlOf('largeCluster') })

test('median frame rate on a large cluster is at least 30 fps', async ({ page }) => {
  test.setTimeout(60_000)
  await page.goto('/')
  const svg = page.getByTestId('web')
  await expect(svg.locator('[data-role="node"]')).not.toHaveCount(0)
  await expect.poll(async () => svg.locator('[data-role="node"]').count(), { timeout: 15_000 }).toBeGreaterThanOrEqual(25)
  const zoom0 = await svg.getAttribute('data-zoom')
  const result = await page.evaluate(async (ms) => {
    const el = document.querySelector('[data-testid="web"]')
    if (!el) throw new Error('no web')
    const box = el.getBoundingClientRect()
    const deltas: number[] = []
    let zooms = 0
    let last = 0
    const seen = new Set<string>()
    await new Promise<void>((done) => {
      const t0 = performance.now()
      const frame = (t: number) => {
        if (last) deltas.push(t - last)
        last = t
        el.dispatchEvent(
          new WheelEvent('wheel', {
            deltaY: deltas.length % 2 ? 10 : -10,
            clientX: box.left + box.width / 2,
            clientY: box.top + box.height / 2,
            bubbles: true,
            cancelable: true,
          }),
        )
        seen.add(el.getAttribute('data-zoom') ?? '')
        zooms++
        if (t - t0 < ms) requestAnimationFrame(frame)
        else done()
      }
      requestAnimationFrame(frame)
    })
    deltas.sort((a, b) => a - b)
    return { median: deltas[Math.floor(deltas.length / 2)] ?? Infinity, frames: deltas.length, zooms, distinct: seen.size }
  }, 10_000)
  const fps = 1000 / result.median
  test.info().annotations.push({ type: 'perf', description: `median ${result.median.toFixed(2)} ms (${fps.toFixed(1)} fps) over ${result.frames} frames` })
  console.log(`perf: median frame ${result.median.toFixed(2)} ms = ${fps.toFixed(1)} fps over ${result.frames} frames`)
  // the probe really re-rendered: the zoom attribute took more than one value while it ran
  expect(result.distinct).toBeGreaterThan(1)
  expect(await svg.getAttribute('data-auto')).toBe('off')
  expect(zoom0).not.toBeNull()
  expect(fps).toBeGreaterThanOrEqual(30)
})
