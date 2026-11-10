import { expect, test } from '@playwright/test'
import { WEB_APP, foreignRequests, recordCspViolations, recordRequests, smallText } from './checks'

// Runs against `visual-kubernetes --replay test/fixtures/streams/deploy-scale.ndjson`
// (web/playwright.config.ts). The replay plays once from the first subscriber; later page
// loads get a snapshot of where it got to, so every assertion waits for the final state and
// none reads a coordinate (ADR-004).

const web = (page: import('@playwright/test').Page) => page.getByTestId('web')

test('draws the spider web from the stream', async ({ page }) => {
  await page.goto('/')
  await expect(web(page).locator('[data-role="hub"][data-id="cp/apiserver"]')).toHaveCount(1)
  await expect(web(page).locator('[data-role="node"]')).toHaveCount(3)
  // the Deployment grows from 2 to 4 pods and every pod lands on a node
  const app = web(page).locator(`[data-role="app"][data-id="${WEB_APP}"]`)
  await expect(app).toHaveAttribute('data-pods', '4', { timeout: 15_000 })
  await expect(app.locator('[data-role="pod"][data-node=""]')).toHaveCount(0, { timeout: 15_000 })
  await expect(app.locator('[data-role="pod"]')).toHaveCount(4)
  // satellites ride on their app
  for (const kind of ['HorizontalPodAutoscaler', 'ConfigMap', 'Service']) {
    await expect(app.locator(`[data-role="sat"][data-kind="${kind}"]`)).toHaveCount(1)
  }
  // the system DaemonSet is drawn with a pod on each node
  const proxy = web(page).locator('[data-role="app"][data-id="app/kube-system/DaemonSet/kube-proxy"]')
  await expect(proxy).toHaveAttribute('data-pods', '3')
  // the API server links to every node; nodes thread to the workloads they host
  await expect(web(page).locator('[data-kind="member"][data-edge^="member:cp/apiserver->core/Node/"]')).toHaveCount(3)
  await expect(web(page).locator(`[data-edge="hosts:core/Node/_/node-3->${WEB_APP}"]`)).toHaveCount(1)
  await expect(web(page).locator('[data-kind="hosts"]')).not.toHaveCount(0)
  // labels name what is drawn
  await expect(web(page).locator(`[data-label-for="${WEB_APP}"] .name`)).toHaveText('web')
  await expect(web(page).locator('[data-label-for="core/Node/_/node-1"] .name')).toHaveText('node-1')
})

test('zoom and pan hand the camera to the presenter; F fits again', async ({ page }) => {
  await page.goto('/')
  const svg = web(page)
  await expect(svg.locator('[data-role="node"]')).toHaveCount(3)
  await expect(svg).toHaveAttribute('data-auto', 'on')
  await page.mouse.move(800, 450)
  await page.mouse.wheel(0, -600)
  await expect(svg).toHaveAttribute('data-auto', 'off')
  const zoomed = Number(await svg.getAttribute('data-zoom'))
  await page.mouse.down()
  await page.mouse.move(600, 300, { steps: 5 })
  await page.mouse.up()
  await expect(svg).toHaveAttribute('data-auto', 'off')
  expect(Number(await svg.getAttribute('data-zoom'))).toBe(zoomed)
  await page.keyboard.press('f')
  await expect(svg).toHaveAttribute('data-auto', 'on')
  await expect.poll(async () => Number(await svg.getAttribute('data-zoom'))).not.toBe(zoomed)
})

test('the text-size check catches small text (must-fail state)', async ({ page }) => {
  await page.goto('/')
  await expect(web(page).locator('[data-role="node"]')).toHaveCount(3)
  await page.evaluate(() => {
    const div = document.createElement('div')
    div.textContent = 'tiny html'
    div.style.fontSize = '10px'
    document.body.append(div)
    const svg = document.querySelector('[data-layer="world"]')
    const t = document.createElementNS('http://www.w3.org/2000/svg', 'text')
    t.textContent = 'tiny zoomed svg'
    t.setAttribute('font-size', '14')
    const g = document.createElementNS('http://www.w3.org/2000/svg', 'g')
    g.setAttribute('transform', 'scale(0.5)')
    g.append(t)
    svg?.append(g)
  })
  const found = (await smallText(page)).map((s) => s.text)
  expect(found).toContain('tiny html')
  expect(found).toContain('tiny zoomed svg')
})

test('no text on the page is under 12px', async ({ page }) => {
  await page.goto('/')
  const app = web(page).locator(`[data-role="app"][data-id="${WEB_APP}"]`)
  await expect(app).toHaveAttribute('data-pods', '4', { timeout: 15_000 })
  await page.getByRole('button', { name: /Showing all/ }).click() // the open filter list counts too
  await expect(page.locator('.f-row')).toHaveCount(8)
  expect(await smallText(page)).toEqual([])
  // zoomed far out, labels keep their size (they live outside the zoomed world)
  await page.mouse.move(800, 450)
  for (let i = 0; i < 6; i++) await page.mouse.wheel(0, 800)
  await expect(web(page)).toHaveAttribute('data-zoom', '0.250')
  expect(await smallText(page)).toEqual([])
})

test('the request checks see what they guard (must-fail state)', async ({ page }) => {
  const urls = recordRequests(page)
  const blocked = await recordCspViolations(page)
  await page.goto('/')
  await expect(web(page).locator('[data-role="node"]')).toHaveCount(3)
  expect(urls.some((u) => u.endsWith('/api/stream'))).toBe(true)
  expect(foreignRequests(['http://203.0.113.9/x.png', 'data:,x'], 'http://127.0.0.1:1')).toEqual([
    'http://203.0.113.9/x.png',
  ])
  await page.evaluate(() => {
    const img = document.createElement('img')
    img.src = 'http://203.0.113.9/x.png'
    document.body.append(img)
  })
  await expect.poll(() => blocked).toContain('http://203.0.113.9/x.png')
})

test('the page makes no request outside its own origin', async ({ page, baseURL }) => {
  const urls = recordRequests(page)
  const blocked = await recordCspViolations(page)
  await page.goto('/')
  const app = web(page).locator(`[data-role="app"][data-id="${WEB_APP}"]`)
  await expect(app).toHaveAttribute('data-pods', '4', { timeout: 15_000 })
  expect(urls.length).toBeGreaterThan(0)
  expect(foreignRequests(urls, new URL(baseURL ?? '').origin)).toEqual([])
  expect(blocked).toEqual([])
})
