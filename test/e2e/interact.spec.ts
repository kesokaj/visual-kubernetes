import { expect, test, type Locator, type Page } from '@playwright/test'
import { smallText, WEB_APP } from './checks'
import { urlOf } from './servers'

// Grouping, neighbourhood fade, zoom controls and drag (plan PLAN-2026-11-11-1703, D1–D3, D7,
// D13). Runs against the deploy-scale replay, which several specs open: every assertion waits
// for the final state (web at 4 pods). Positions are read from screen-space label x, which is
// world x × zoom + pan (labels.tsx), so a drag can be measured without reading the world.

const HUB = 'cp/apiserver'
const PROXY = 'app/kube-system/DaemonSet/kube-proxy'

test.use({ baseURL: urlOf('deployScale') })

const web = (page: Page) => page.getByTestId('web')
const body = (page: Page, id: string) => web(page).locator(`[data-layer="bodies"] > g[data-id="${id}"]`)
const icon = (page: Page, id: string) => body(page, id).locator(':scope > use')
const zoomOf = async (page: Page) => Number(await web(page).getAttribute('data-zoom'))
// stableZoom is the zoom once a glide has landed (unchanged over 300 ms): a poll that passes
// mid-glide would otherwise read a value the camera is still leaving (1.119 vs > 1.1258 flake).
async function stableZoom(page: Page): Promise<number> {
  let last = NaN
  await expect
    .poll(async () => {
      const k = await zoomOf(page)
      const still = Math.abs(k - last) < 0.0005
      last = k
      await page.waitForTimeout(300)
      return still
    })
    .toBe(true)
  return last
}
const labelX = async (page: Page, id: string) =>
  Number(await web(page).locator(`[data-label-for="${id}"] text.name`).getAttribute('x'))

async function ready(page: Page) {
  await page.goto('/')
  await expect(body(page, WEB_APP)).toHaveAttribute('data-pods', '4', { timeout: 15_000 })
}

async function centre(l: Locator) {
  const b = await l.boundingBox()
  if (!b) throw new Error('not drawn')
  return { x: b.x + b.width / 2, y: b.y + b.height / 2 }
}

// settle waits until the web stops moving (label x unchanged over 400 ms).
async function settle(page: Page, id: string) {
  let last = NaN
  await expect
    .poll(
      async () => {
        const x = await labelX(page, id)
        const still = Math.abs(x - last) < 0.5
        last = x
        await page.waitForTimeout(400)
        return still
      },
      { timeout: 20_000 },
    )
    .toBe(true)
}

// emptyPoint is a screen point on the grid paper with no body under it, away from the pill.
async function emptyPoint(page: Page) {
  const p = await page.evaluate(() => {
    for (let y = 120; y < 760; y += 40) {
      for (let x = 260; x < 1500; x += 40) {
        const el = document.elementFromPoint(x, y)
        if (el && el.closest('[data-testid="web"]') && !el.closest('[data-layer="bodies"] [data-id]')) return { x, y }
      }
    }
    return null
  })
  if (!p) throw new Error('no empty point')
  return p
}

test('each app with riders draws one group disc and a tether spoke per pod and satellite', async ({ page }) => {
  await ready(page)
  for (const id of [WEB_APP, PROXY]) {
    const app = body(page, id)
    const riders = (await app.locator('[data-role="pod"]').count()) + (await app.locator('[data-role="sat"]').count())
    expect(riders).toBeGreaterThan(0)
    await expect(app.locator(':scope > .group-disc')).toHaveCount(1)
    await expect(app.locator(':scope > .tether')).toHaveAttribute('data-spokes', String(riders))
  }
  await expect(body(page, WEB_APP).locator(':scope > .tether')).toHaveAttribute('data-spokes', '7') // 4 pods + HPA, ConfigMap, Service
})

test('hover fades what does not work with the app; a click keeps it; Esc clears it', async ({ page }) => {
  await ready(page)
  const faded = web(page).locator('[data-faded="true"]')
  await expect(faded).toHaveCount(0)

  await icon(page, WEB_APP).hover({ force: true })
  await expect(body(page, PROXY)).toHaveAttribute('data-faded', 'true')
  await expect(web(page).locator(`[data-label-for="${PROXY}"]`)).toHaveAttribute('data-faded', 'true')
  for (const id of [WEB_APP, HUB, 'core/Node/_/node-3']) await expect(body(page, id)).not.toHaveAttribute('data-faded', 'true')
  await expect(web(page).locator(`[data-edge="hosts:core/Node/_/node-3->${WEB_APP}"]`)).not.toHaveAttribute('data-faded', 'true')

  const away = await emptyPoint(page)
  await page.mouse.move(away.x, away.y)
  await expect(faded).toHaveCount(0)

  // A click selects: the fade stays after the pointer leaves, until Esc closes the panel.
  await icon(page, WEB_APP).click({ force: true })
  await expect(page.getByTestId('panel')).toHaveAttribute('data-panel-for', WEB_APP)
  await page.mouse.move(away.x, away.y)
  await expect(body(page, PROXY)).toHaveAttribute('data-faded', 'true')
  await page.keyboard.press('Escape')
  await expect(faded).toHaveCount(0)
})

test('zoom buttons, + / − keys and double-click zoom; Fit hands the camera back', async ({ page }) => {
  await ready(page)
  await expect(web(page)).toHaveAttribute('data-auto', 'on')
  // The baseline must be the settled fit: mid-settle, auto-fit is still easing (verifier flake 1.042 vs 1.096).
  await settle(page, WEB_APP)
  const start = await zoomOf(page)

  // R6/D2: the zoom buttons are part of the one filter pill, not a second floating pill.
  const zoomShadow = await page.locator('.f-zoom').evaluate((el) => getComputedStyle(el).boxShadow)
  expect(zoomShadow).toBe('none')
  await expect(page.locator('.f-bar').filter({ has: page.locator('.f-pill') }).locator('.f-zoom')).toHaveCount(1)

  await page.getByRole('button', { name: 'Zoom in' }).click()
  await expect(web(page)).toHaveAttribute('data-auto', 'off')
  await expect.poll(() => zoomOf(page)).toBeGreaterThan(start * 1.3)

  await page.getByRole('button', { name: 'Fit' }).click()
  await expect(web(page)).toHaveAttribute('data-auto', 'on')
  await expect.poll(() => zoomOf(page)).toBeCloseTo(start, 1)

  await page.keyboard.press('-')
  await expect(web(page)).toHaveAttribute('data-auto', 'off')
  await expect.poll(() => zoomOf(page)).toBeLessThan(start * 0.8)
  const out = await stableZoom(page)
  await page.keyboard.press('+')
  await expect.poll(() => zoomOf(page)).toBeGreaterThan(out * 1.3)

  const before = await stableZoom(page)
  const at = await centre(icon(page, WEB_APP))
  await page.mouse.dblclick(at.x, at.y)
  await expect.poll(() => zoomOf(page)).toBeGreaterThan(before * 1.5)
})

test('dragging an app moves it under the pointer without selecting; empty space still pans', async ({ page }) => {
  await ready(page)
  await settle(page, WEB_APP)
  const from = await centre(icon(page, WEB_APP))
  const appX = await labelX(page, WEB_APP)
  const hubX = await labelX(page, HUB)

  await page.mouse.move(from.x, from.y)
  await page.mouse.down()
  await page.mouse.move(from.x + 160, from.y + 60, { steps: 12 })
  await expect(web(page)).toHaveAttribute('data-auto', 'off')
  // Pinned under the pointer while held: the label follows the 160 px move.
  await expect.poll(async () => Math.abs((await labelX(page, WEB_APP)) - appX - 160)).toBeLessThan(40)
  expect(Math.abs((await labelX(page, HUB)) - hubX)).toBeLessThan(1) // the hub stays put
  await page.mouse.up()
  await expect(page.getByTestId('panel')).toHaveCount(0)

  // Empty space still pans: everything, the hub included, moves on screen.
  const blank = await emptyPoint(page)
  const hubBefore = await labelX(page, HUB)
  await page.mouse.move(blank.x, blank.y)
  await page.mouse.down()
  await page.mouse.move(blank.x - 120, blank.y, { steps: 8 })
  await page.mouse.up()
  await expect.poll(async () => hubBefore - (await labelX(page, HUB))).toBeGreaterThan(100)
  await expect(page.getByTestId('panel')).toHaveCount(0)
})

test('the hub cannot be dragged: pressing it pans the whole web', async ({ page }) => {
  await ready(page)
  await settle(page, WEB_APP)
  const hub = await centre(body(page, HUB).locator(':scope > use'))
  const hubX = await labelX(page, HUB)
  const appX = await labelX(page, WEB_APP)
  await page.mouse.move(hub.x, hub.y)
  await page.mouse.down()
  await page.mouse.move(hub.x + 140, hub.y, { steps: 10 })
  await page.mouse.up()
  // A pan moves the hub and the app alike; a hub drag would leave the pinned hub in place.
  await expect.poll(async () => (await labelX(page, HUB)) - hubX).toBeGreaterThan(120)
  expect(Math.abs((await labelX(page, WEB_APP)) - appX - ((await labelX(page, HUB)) - hubX))).toBeLessThan(20)
})

test('hover shows the name and kind of any object; leaving clears it', async ({ page }) => {
  await ready(page)
  await settle(page, WEB_APP)
  const label = web(page).getByTestId('hover-label')
  await expect(label).toHaveCount(0)

  const pod = body(page, WEB_APP).locator('[data-role="pod"]').first()
  const podId = await pod.getAttribute('data-id')
  await pod.hover({ force: true })
  await expect(label).toHaveAttribute('data-for', podId ?? '')
  await expect(label).toContainText(podId?.split('/').pop() ?? '')
  await expect(label).toContainText('Pod · shop')
  expect(await smallText(page)).toEqual([]) // R10 holds with the card shown

  await icon(page, WEB_APP).hover({ force: true })
  await expect(label).toHaveAttribute('data-for', WEB_APP)
  await expect(label).toContainText('Deployment · shop')

  const svc = body(page, WEB_APP).locator('[data-role="sat"][data-kind="Service"]')
  await svc.hover({ force: true })
  await expect(label).toHaveAttribute('data-for', (await svc.getAttribute('data-id')) ?? '')
  await expect(label).toContainText('Service · shop')

  await body(page, HUB).locator(':scope > use').hover({ force: true })
  await expect(label).toHaveAttribute('data-for', HUB)

  const away = await emptyPoint(page)
  await page.mouse.move(away.x, away.y)
  await expect(label).toHaveCount(0)
})
