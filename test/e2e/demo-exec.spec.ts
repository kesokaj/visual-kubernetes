import { expect, test, type Page } from '@playwright/test'
import { WEB_APP, foreignRequests, recordCspViolations, recordRequests, smallText } from './checks'
import { urlOf } from './servers'

const web = (page: Page) => page.getByTestId('web')
const panel = (page: Page) => page.getByTestId('panel')

async function clickBody(page: Page, id: string) {
  const close = panel(page).getByRole('button', { name: 'Close panel' })
  if (await close.isVisible()) await close.click()
  await web(page).locator(`[data-layer="bodies"] > g[data-id="${id}"] > use`).first().click()
}

async function clickSatellite(page: Page, id: string) {
  const close = panel(page).getByRole('button', { name: 'Close panel' })
  if (await close.isVisible()) await close.click()
  await web(page).locator(`[data-layer="bodies"] g[data-id="${id}"] > use`).first().click()
}

test.describe('demo pod exec off by default', () => {
  test.use({ baseURL: urlOf('telemetry') })

  test('hides pod terminal controls and refuses /api/demo/exec with 403', async ({ page, request }) => {
    const res = await request.get('/api/demo/exec?id=core/Pod/shop/web-0&shell=/bin/sh')
    expect(res.status()).toBe(403)
    expect(await res.text()).toContain('demo mode is off on this install')

    await page.goto('/')
    await expect(web(page).locator(`[data-layer="bodies"] > g[data-id="${WEB_APP}"]`)).toHaveCount(1)
    await clickBody(page, WEB_APP)
    await expect(panel(page).locator('[data-section="demo-exec"]')).toHaveCount(0)
  })
})

test.describe('interactive demo pod exec terminal (--demo)', () => {
  test.use({ baseURL: urlOf('demo') })

  test('executes shell commands in running pods, shows clean distroless notice, and blocks system namespaces', async ({
    page,
    request,
  }) => {
    const origin = urlOf('demo')
    const urls = recordRequests(page)
    const cspBlocked = await recordCspViolations(page)

    // System namespace pod is refused with 403 before WebSocket upgrade.
    const sysRes = await request.get('/api/demo/exec?id=core/Pod/kube-system/coredns-0&shell=/bin/sh', {
      headers: { Origin: origin },
    })
    expect(sysRes.status()).toBe(403)
    expect(await sysRes.text()).toContain('protected namespace')

    // Missing SameOrigin header is refused with 403.
    const noOriginRes = await request.get('/api/demo/exec?id=core/Pod/shop/web-0&shell=/bin/sh')
    expect(noOriginRes.status()).toBe(403)

    await page.goto('/')
    await expect(web(page).locator(`[data-layer="bodies"] > g[data-id="${WEB_APP}"]`)).toHaveCount(1)

    // System workload, system pod, and system Service render an explicit protected-namespace notice.
    await clickBody(page, 'app/kube-system/Deployment/coredns')
    await expect(panel(page).locator('[data-section="demo-exec"]')).toBeVisible()
    await expect(panel(page).locator('[data-section="demo-exec"]').getByTestId('exec-protected')).toContainText(
      'System namespace is read-only',
    )
    await panel(page).locator('button[data-pod="core/Pod/kube-system/coredns-0"]').click()
    await expect(panel(page)).toHaveAttribute('data-panel-for', 'core/Pod/kube-system/coredns-0')
    await expect(panel(page).locator('[data-section="demo-exec"]')).toBeVisible()
    await expect(panel(page).locator('[data-section="demo-exec"]').getByTestId('exec-protected')).toContainText(
      'System namespace is read-only',
    )
    await clickSatellite(page, 'core/Service/kube-system/kube-dns')
    await expect(panel(page)).toHaveAttribute('data-panel-for', 'core/Service/kube-system/kube-dns')
    await expect(panel(page).locator('[data-section="demo-exec"]').getByTestId('exec-protected')).toContainText(
      'System namespace is read-only',
    )

    // Zero-pod workload explains why Pod shell is unavailable instead of hiding the section.
    await clickBody(page, 'app/shop/Deployment/idle')
    await expect(panel(page).locator('[data-section="demo-exec"]').getByTestId('exec-no-pods')).toContainText(
      'No Running pods yet — scale replicas above 0',
    )

    // Non-system Service satellite lists backing pods' /bin/sh launch chips and opens the terminal.
    await clickSatellite(page, 'core/Service/shop/web')
    await expect(panel(page)).toHaveAttribute('data-panel-for', 'core/Service/shop/web')
    await expect(panel(page).locator('[data-section="demo-exec"]').getByTestId('exec-pod-web-0')).toBeVisible()

    // Non-system pods on a Node show direct /bin/sh chips while omitting system pods.
    await clickBody(page, 'core/Node/_/node-1')
    const nodeExec = panel(page).locator('[data-section="demo-exec"]')
    await expect(nodeExec).toBeVisible()
    await expect(nodeExec.locator('button[data-exec-pod="core/Pod/shop/web-0"]')).toBeVisible()
    await expect(nodeExec.locator('button[data-exec-pod="core/Pod/kube-system/coredns-0"]')).toHaveCount(0)

    // Select shop/web -> select pod web-0 -> open /bin/sh terminal.
    await clickBody(page, WEB_APP)
    await expect(panel(page).locator('[data-section="demo-exec"]')).toBeVisible()
    await panel(page).locator('button[data-pod="core/Pod/shop/web-0"]').click()
    await expect(panel(page)).toHaveAttribute('data-panel-for', 'core/Pod/shop/web-0')
    await panel(page).getByTestId('exec-open-sh').click()

    const drawer = page.getByTestId('exec-drawer')
    await expect(drawer).toBeVisible()
    await expect(drawer.getByTestId('exec-status')).toHaveText('Connected')
    await expect(drawer).toContainText('Connected to shop/web-0 (/bin/sh)')

    // Focus terminal and execute a command round-trip.
    await drawer.locator('.xterm-helper-textarea').focus()
    await page.keyboard.type('echo hello-e2e\n')
    await expect(drawer).toContainText('hello-e2e')
    expect(await smallText(page)).toEqual([])

    // Switch to worker-0 and select the distroless sidecar container to verify clean missing-shell UX.
    await clickBody(page, 'app/shop/Deployment/worker')
    await panel(page).getByTestId('exec-pod-worker-0').click()
    await expect(drawer).toContainText('Connected to shop/worker-0 (/bin/sh)')
    const distrolessTab = drawer.getByTestId('exec-container-distroless-helper')
    await expect(distrolessTab).toBeVisible()
    await distrolessTab.click()

    const exitBanner = drawer.getByTestId('exec-exit')
    await expect(exitBanner).toBeVisible()
    await expect(exitBanner).toContainText('Container has no shell')
    expect(await smallText(page)).toEqual([])

    // Escape closes the terminal drawer and panel.
    await page.keyboard.press('Escape')
    await expect(drawer).toHaveCount(0)

    expect(foreignRequests(urls, origin)).toEqual([])
    expect(cspBlocked).toEqual([])
  })

  test('surfaces terminal chunk load failure with Reconnect instead of hanging in Connecting', async ({ page }) => {
    let failChunk = true
    await page.route('**/assets/xterm-*.js', async (route) => {
      if (failChunk) {
        await route.abort('failed')
        return
      }
      await route.continue()
    })

    await page.goto('/')
    await expect(web(page).locator(`[data-layer="bodies"] > g[data-id="${WEB_APP}"]`)).toHaveCount(1)
    await clickBody(page, WEB_APP)
    await panel(page).locator('button[data-pod="core/Pod/shop/web-0"]').click()
    await panel(page).getByTestId('exec-open-sh').click()

    const drawer = page.getByTestId('exec-drawer')
    await expect(drawer).toBeVisible()
    await expect(drawer.getByTestId('exec-status')).toHaveText('Disconnected')
    await expect(drawer.getByTestId('exec-exit')).toBeVisible()
    await expect(drawer.getByTestId('exec-reconnect')).toBeVisible()
    expect(await smallText(page)).toEqual([])

    failChunk = false
    await drawer.getByTestId('exec-reconnect').click()
    await expect(drawer.getByTestId('exec-status')).toHaveText('Connected')
  })
})
