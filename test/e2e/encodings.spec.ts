import { expect, test, type Page } from '@playwright/test'
import { urlOf } from './servers'

// Size, halo, pulse and travel encodings (plan Phase 4, PRD S2/S3). A replay plays once from
// its first subscriber, so each delta test is the first and only opener of its server
// (test/e2e/servers.ts) and loads the page before the deltas arrive. Assertions read data-*
// attributes, never coordinates (ADR-004).

const web = (page: Page) => page.getByTestId('web')
const body = (page: Page, id: string) => web(page).locator(`[data-layer="bodies"] > g[data-id="${id}"]`)

test.describe('missing sources: sizes fall back to pod counts', () => {
  test.use({ baseURL: urlOf('missingSources') })

  test('a new app pulses, a deleted pod pulses it again, pod counts size and rank apps', async ({ page }) => {
    await page.goto('/')
    // metrics.k8s.io is missing and no metrics event arrives: size by pod count
    await expect(web(page)).toHaveAttribute('data-size-by', 'pods')
    const api = body(page, 'app/shop/Deployment/api')
    await expect(api).toHaveCount(1, { timeout: 10_000 })
    // the new app pulses, and its ring shows while the pulse runs
    await expect.poll(async () => Number(await api.getAttribute('data-pulses'))).toBeGreaterThan(0)
    const first = Number(await api.getAttribute('data-pulses'))
    // at 2000 ms one of its two pods is deleted: one more pulse, one pod left
    await expect(api).toHaveAttribute('data-pods', '1', { timeout: 10_000 })
    await expect.poll(async () => Number(await api.getAttribute('data-pulses'))).toBeGreaterThan(first)
    // the busiest apps by pod count carry the top-3 halo, and the halo count matches
    const ranked = web(page).locator('[data-role="app"][data-top]')
    await expect(ranked).not.toHaveCount(0)
    expect(await ranked.count()).toBeLessThanOrEqual(3)
    await expect(web(page).locator('[data-role="app"][data-top="1"] > .halo')).toHaveCount(1)
    await expect(web(page).locator('.halo')).toHaveCount(await ranked.count())
  })
})

test.describe('node join: pods travel along their thread', () => {
  test.use({ baseURL: urlOf('nodeJoin') })

  test('the kube-proxy pod placed on node-3 travels from the node to its app', async ({ page }) => {
    await page.goto('/')
    const pod = 'core/Pod/kube-system/kube-proxy-node-3'
    const proxy = 'app/kube-system/DaemonSet/kube-proxy'
    const travel = web(page).locator(`[data-layer="travels"] > g[data-travel="${pod}"]`)
    // the pod arrives at 3500 ms already bound to node-3 and moves for 1.8 s
    await expect(travel).toHaveCount(1, { timeout: 10_000 })
    await expect(travel).toHaveAttribute('data-node', 'core/Node/_/node-3')
    await expect(travel).toHaveAttribute('data-app', proxy)
    // while it travels, its ring slot fades in; afterwards it sits on the ring for good
    await expect(travel).toHaveCount(0, { timeout: 10_000 })
    const onRing = body(page, proxy).locator(`[data-role="pod"][data-id="${pod}"]`)
    await expect(onRing).toHaveAttribute('data-node', 'node-3')
    await expect(onRing).not.toHaveClass(/arriving/)
    await expect.poll(async () => Number(await body(page, proxy).getAttribute('data-pulses'))).toBeGreaterThan(0)
  })
})

test.describe('telemetry: sizes follow throughput', () => {
  test.use({ baseURL: urlOf('telemetry') })

  test('the busiest app is the biggest, ranks first and has a halo; exactly three halos', async ({ page }) => {
    await page.goto('/')
    await expect(web(page)).toHaveAttribute('data-size-by', 'network', { timeout: 10_000 })
    const webApp = body(page, 'app/shop/Deployment/web')
    const batch = body(page, 'app/shop/Deployment/batch')
    await expect(webApp).toHaveAttribute('data-top', '1')
    await expect(body(page, 'app/shop/Deployment/api')).toHaveAttribute('data-top', '2')
    await expect(batch).not.toHaveAttribute('data-top', /.+/)
    expect(Number(await webApp.getAttribute('data-size'))).toBeGreaterThan(Number(await batch.getAttribute('data-size')))
    await expect(web(page).locator('.halo')).toHaveCount(3)
  })
})
