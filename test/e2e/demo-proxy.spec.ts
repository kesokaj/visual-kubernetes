import { expect, test, type Frame, type Page } from '@playwright/test'
import { WEB_APP, foreignRequests, recordCspViolations, recordRequests, smallText } from './checks'
import { urlOf } from './servers'

const web = (page: Page) => page.getByTestId('web')
const panel = (page: Page) => page.getByTestId('panel')

async function clickBody(page: Page, id: string) {
  const close = panel(page).getByRole('button', { name: 'Close panel' })
  if (await close.isVisible()) await close.click()
  await web(page).locator(`[data-layer="bodies"] > g[data-id="${id}"] > use`).first().click()
}

async function smallTextInFrame(frame: Frame, minPx = 12): Promise<{ text: string; px: number }[]> {
  return frame.evaluate((min) => {
    const out: { text: string; px: number }[] = []
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT)
    for (let n = walker.nextNode(); n; n = walker.nextNode()) {
      const text = n.textContent?.trim() ?? ''
      const el = n.parentElement
      if (!text || !el) continue
      const px = parseFloat(getComputedStyle(el).fontSize)
      if (px > 0 && px < min) out.push({ text: text.slice(0, 40), px })
    }
    return out
  }, minPx)
}

test.describe('demo mode off by default', () => {
  test.use({ baseURL: urlOf('telemetry') })

  test('hides proxy controls and refuses /api/demo/* with 403', async ({ page, request }) => {
    const res = await request.get('/api/demo/ports')
    expect(res.status()).toBe(403)
    expect(await res.text()).toContain('demo mode is off on this install')

    await page.goto('/')
    await expect(web(page).locator(`[data-layer="bodies"] > g[data-id="${WEB_APP}"]`)).toHaveCount(1)
    await clickBody(page, WEB_APP)
    await expect(panel(page)).toHaveAttribute('data-panel-for', WEB_APP)
    await expect(panel(page).locator('[data-section="demo-proxy"]')).toHaveCount(0)
  })
})

test.describe('interactive demo controlled proxy (--demo)', () => {
  test.use({ baseURL: urlOf('demo') })

  test('starts closed, opens in a sandboxed view, follows links and forms, and revokes on close', async ({
    page,
    request,
  }) => {
    const origin = urlOf('demo')
    const urls = recordRequests(page)
    const cspBlocked = await recordCspViolations(page)

    // Port starts closed and unknown token returns 403.
    const initPortsRes = await request.get('/api/demo/ports')
    expect(initPortsRes.status()).toBe(200)
    const initPorts = (await initPortsRes.json()) as {
      ports: { namespace: string; service: string; port: number; open: boolean; pathPrefix: string }[]
    }
    expect(initPorts.ports).toEqual([
      expect.objectContaining({ namespace: 'shop', service: 'web', port: 80, open: false, pathPrefix: '' }),
    ])
    const closedTry = await request.get('/api/demo/proxy/t/00000000000000000000000000000000/')
    expect(closedTry.status()).toBe(403)

    // System namespace open is refused with 403 even with matching Origin.
    const sysRes = await request.post('/api/demo/ports', {
      headers: { Origin: origin },
      data: { namespace: 'kube-system', service: 'kube-dns', port: 53, open: true },
    })
    expect(sysRes.status()).toBe(403)

    await page.goto('/')
    await expect(web(page).locator(`[data-layer="bodies"] > g[data-id="${WEB_APP}"]`)).toHaveCount(1)

    // API server panel shows demo capability ok.
    await clickBody(page, 'cp/apiserver')
    await expect(panel(page).locator('li[data-cap="demo"]')).toHaveAttribute('data-state', 'ok')

    // Non-exposed workload has no proxy section; system workload has no proxy section.
    await clickBody(page, 'app/shop/Deployment/worker')
    await expect(panel(page).locator('[data-section="demo-proxy"]')).toHaveCount(0)
    await clickBody(page, 'app/kube-system/Deployment/coredns')
    await expect(panel(page).locator('[data-section="demo-proxy"]')).toHaveCount(0)

    // Internet globe lists all exposed cluster Service ports.
    await web(page).locator('[data-layer="bodies"] > g[data-id="ext/internet"] > g.globe').click()
    await expect(panel(page).locator('[data-section="demo-proxy"] li[data-demo-port="shop/web:80"]')).toHaveCount(1)

    // Select exposed shop/web app and open port 80 in the sandboxed browser sheet.
    await clickBody(page, WEB_APP)
    const proxySec = panel(page).locator('[data-section="demo-proxy"]')
    const row = proxySec.locator('li[data-demo-port="shop/web:80"]')
    await expect(row).toHaveAttribute('data-open', 'false')

    await row.locator('[data-action="open-port"]').click()
    await expect(row).toHaveAttribute('data-open', 'true')

    const sheet = page.getByTestId('demo-browser')
    await expect(sheet).toBeVisible()
    const iframeEl = page.getByTestId('demo-iframe')
    await expect(iframeEl).toHaveAttribute('sandbox', 'allow-scripts allow-forms')
    const srcAttr = await iframeEl.getAttribute('src')
    expect(srcAttr).toMatch(/^\/api\/demo\/proxy\/t\/[0-9a-f]{32}\/$/)

    const frame = page.frameLocator('[data-testid="demo-iframe"]')
    await expect(frame.locator('h1')).toContainText('web.shop.svc')
    expect(await smallText(page)).toEqual([])

    const childFrame = page.frames().find((f) => f.url().includes('/api/demo/proxy/t/'))
    expect(childFrame).toBeDefined()
    if (childFrame) {
      expect(await smallTextInFrame(childFrame)).toEqual([])
    }

    // Internal link navigation stays inside the rewritten proxy prefix.
    await frame.locator('#nav-about').click()
    await expect(frame.locator('#about-copy')).toContainText('Deterministic replay upstream')
    await frame.locator('#nav-home').click()
    await expect(frame.locator('#echo-form')).toBeVisible()

    // Form POST from opaque-origin sandboxed iframe succeeds through the capability token.
    await frame.locator('#msg').fill('hello from sandbox')
    await frame.locator('#echo-submit').click()
    await expect(frame.locator('#echo-out')).toContainText('Echo: hello from sandbox')

    // Closing the port hides the browser view and immediately revokes the token with 403.
    await row.locator('[data-action="close-port"]').click()
    await expect(row).toHaveAttribute('data-open', 'false')
    await expect(sheet).toHaveCount(0)

    const revokedTry = await request.get(srcAttr ?? '/api/demo/proxy/t/revoked/')
    expect(revokedTry.status()).toBe(403)

    expect(foreignRequests(urls, origin)).toEqual([])
    expect(cspBlocked).toEqual([])
  })
})
