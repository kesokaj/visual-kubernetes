import { expect, test, type Page } from '@playwright/test'
import { urlOf } from './servers'

// Perf probe at G3 scale (PRD G3/S5, plan D41/D42): g3-scale.ndjson is one snapshot of 150 apps
// and 1,000 pods on 4 nodes (5 of the apps are scaled to zero and fold into the over-budget
// counter, so exactly 150 bubbles are drawn). At 1600×900 in headless Chromium the median
// frame time over 10 s stays at or under 33.3 ms (≥ 30 fps). Every frame is made to re-render:
// a wheel event each frame zooms the camera in and out, so the probe measures drawing the whole
// web, not an idle page. The second test is the probe's must-fail state: with the CPU
// throttled 20× through CDP the same measurement must fall under 30 fps.

test.use({ baseURL: urlOf('g3Scale') })

const BUBBLES = 150
const PODS = 1000
const ANCHORS = '[data-layer="bodies"] > g[data-id]'

type Probe = { median: number; frames: number; distinct: number }

// openAtScale loads the G3 snapshot and checks the whole web is drawn before anything is timed.
async function openAtScale(page: Page) {
  await page.goto('/')
  const svg = page.getByTestId('web')
  await expect(svg).toHaveAttribute('data-bubbles', String(BUBBLES), { timeout: 20_000 })
  await expect(svg.locator(ANCHORS)).toHaveCount(BUBBLES)
  await expect(svg.locator('[data-role="pod"]')).toHaveCount(PODS)
  const over = page.getByTestId('counters').locator('[data-counter="counter/page.overBudget"]')
  await expect(over).toHaveAttribute('data-count', '5')
}

// probe drives one wheel zoom per animation frame for ms and returns the median frame time.
function probe(page: Page, ms: number): Promise<Probe> {
  return page.evaluate(async (ms) => {
    const el = document.querySelector('[data-testid="web"]')
    if (!el) throw new Error('no web')
    const box = el.getBoundingClientRect()
    const deltas: number[] = []
    let last = 0
    const seen = new Set<string>()
    await new Promise<void>((done) => {
      const t0 = performance.now()
      const frame = (t: number) => {
        if (last) deltas.push(t - last)
        last = t
        const deltaY = deltas.length % 2 ? 10 : -10
        const at = { clientX: box.left + box.width / 2, clientY: box.top + box.height / 2 }
        el.dispatchEvent(new WheelEvent('wheel', { deltaY, ...at, bubbles: true, cancelable: true }))
        seen.add(el.getAttribute('data-zoom') ?? '')
        if (t - t0 < ms) requestAnimationFrame(frame)
        else done()
      }
      requestAnimationFrame(frame)
    })
    deltas.sort((a, b) => a - b)
    return { median: deltas[Math.floor(deltas.length / 2)] ?? Infinity, frames: deltas.length, distinct: seen.size }
  }, ms)
}

function report(label: string, r: Probe): number {
  const fps = 1000 / r.median
  const line = `${label}: median frame ${r.median.toFixed(2)} ms = ${fps.toFixed(1)} fps over ${r.frames} frames`
  test.info().annotations.push({ type: 'perf', description: line })
  console.log(line)
  return fps
}

test('median frame rate at G3 scale (150 bubbles, 1,000 pods) is at least 30 fps', async ({ page }) => {
  test.setTimeout(90_000)
  await openAtScale(page)
  const r = await probe(page, 10_000)
  const fps = report('perf', r)
  // the probe really re-rendered: the zoom took more than one value and auto-fit let go
  expect(r.distinct).toBeGreaterThan(1)
  expect(await page.getByTestId('web').getAttribute('data-auto')).toBe('off')
  expect(fps).toBeGreaterThanOrEqual(30)
})

test('must-fail: the same probe with the CPU throttled 20× falls under 30 fps', async ({ page }) => {
  test.setTimeout(120_000)
  await openAtScale(page)
  const cdp = await page.context().newCDPSession(page)
  await cdp.send('Emulation.setCPUThrottlingRate', { rate: 20 })
  try {
    const r = await probe(page, 10_000)
    const fps = report('perf throttled 20×', r)
    expect(r.distinct).toBeGreaterThan(1)
    expect(fps).toBeLessThan(30)
  } finally {
    await cdp.send('Emulation.setCPUThrottlingRate', { rate: 1 })
  }
})
