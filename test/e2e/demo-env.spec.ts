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

test.describe('demo workload env off by default', () => {
  test.use({ baseURL: urlOf('telemetry') })

  test('hides env editor and refuses /api/demo/workloads/env with 403', async ({ page, request }) => {
    const res = await request.get('/api/demo/workloads/env?id=apps/Deployment/shop/web')
    expect(res.status()).toBe(403)
    expect(await res.text()).toContain('demo mode is off on this install')

    await page.goto('/')
    await expect(web(page).locator(`[data-layer="bodies"] > g[data-id="${WEB_APP}"]`)).toHaveCount(1)
    await clickBody(page, WEB_APP)
    await expect(panel(page)).toHaveAttribute('data-panel-for', WEB_APP)
    await expect(panel(page).locator('[data-section="demo-env"]')).toHaveCount(0)
  })
})

test.describe('interactive demo workload env editor (--demo)', () => {
  test.use({ baseURL: urlOf('demo') })

  test('edits and adds plain env vars across workloads, locks Secret-backed vars, and handles 409 conflicts', async ({
    page,
    request,
  }) => {
    const origin = urlOf('demo')
    const urls = recordRequests(page)
    const cspBlocked = await recordCspViolations(page)

    // System namespace is refused on both GET and PATCH.
    const sysGet = await request.get('/api/demo/workloads/env?id=apps/Deployment/kube-system/coredns')
    expect(sysGet.status()).toBe(403)
    const sysPatch = await request.patch('/api/demo/workloads/env', {
      headers: { Origin: origin },
      data: {
        id: 'apps/Deployment/kube-system/coredns',
        resourceVersion: '1',
        container: 'coredns',
        env: [],
      },
    })
    expect(sysPatch.status()).toBe(403)

    await page.goto('/')
    await expect(web(page).locator('[data-layer="bodies"] > g[data-id="app/shop/Deployment/worker"]')).toHaveCount(1)

    // System workload and non-workload bodies hide the env editor.
    await clickBody(page, 'app/kube-system/Deployment/coredns')
    await expect(panel(page).locator('[data-section="demo-env"]')).toHaveCount(0)
    await clickBody(page, 'cp/apiserver')
    await expect(panel(page).locator('[data-section="demo-env"]')).toHaveCount(0)

    // StatefulSet and DaemonSet workloads render the env editor; multi-container DaemonSet locks tabs while saving.
    await clickBody(page, 'app/shop/StatefulSet/db')
    await expect(panel(page).locator('[data-section="demo-env"] li[data-env-name="LOG_LEVEL"] input[data-field="env-value"]')).toHaveValue('info')

    await clickBody(page, 'app/shop/DaemonSet/agent')
    const dsSec = panel(page).locator('[data-section="demo-env"]')
    await expect(dsSec).toBeVisible()
    const exporterTab = dsSec.locator('button[data-container="metrics-exporter"]')
    await expect(exporterTab).toBeVisible()
    await dsSec.locator('li[data-env-name="LOG_LEVEL"] input[data-field="env-value"]').fill('debug')

    let releasePatch: (() => void) | undefined
    const patchGate = new Promise<void>((resolve) => {
      releasePatch = resolve
    })
    await page.route('**/api/demo/workloads/env', async (route) => {
      if (route.request().method() === 'PATCH') {
        await patchGate
      }
      await route.continue()
    })
    await dsSec.locator('[data-action="save-env"]').click()
    await expect(exporterTab).toBeDisabled()
    releasePatch?.()
    await expect(dsSec.getByTestId('demo-env-saved')).toBeVisible()
    await page.unroute('**/api/demo/workloads/env')

    await exporterTab.click()
    await expect(dsSec.locator('li[data-env-name="METRICS_PORT"] input[data-field="env-value"]')).toHaveValue('9100')

    // Malformed array item fails closed instead of silently dropping an env entry.
    await page.route('**/api/demo/workloads/env?id=apps%2FStatefulSet%2Fshop%2Fdb', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          id: 'apps/StatefulSet/shop/db',
          resourceVersion: '1',
          containers: [{ name: 'db', env: [{ name: 'LOG_LEVEL', value: 'info' }, { broken: true }] }],
        }),
      })
    })
    await clickBody(page, 'app/shop/StatefulSet/db')
    await expect(panel(page).locator('[data-section="demo-env"]').getByTestId('demo-env-error')).toContainText(
      'Malformed workload environment response',
    )
    await page.unroute('**/api/demo/workloads/env?id=apps%2FStatefulSet%2Fshop%2Fdb')

    // Select Deployment shop/worker: inspect plain LOG_LEVEL and locked Secret-backed API_SECRET.
    await clickBody(page, 'app/shop/Deployment/worker')
    const envSec = panel(page).locator('[data-section="demo-env"]')
    await expect(envSec).toBeVisible()

    const lockedRow = envSec.locator('li[data-env-locked="API_SECRET"]')
    await expect(lockedRow).toBeVisible()
    await expect(lockedRow.locator('[data-readonly-source="secret:worker-secrets/api-key"]')).toContainText(
      'secret:worker-secrets/api-key',
    )
    await expect(lockedRow.locator('input')).toHaveCount(0)

    // Edit LOG_LEVEL and add CACHE_TTL, then save.
    const logInput = envSec.locator('li[data-env-name="LOG_LEVEL"] input[data-field="env-value"]')
    await expect(logInput).toHaveValue('info')
    await logInput.fill('debug')

    await envSec.locator('[data-action="add-env"]').click()
    const newRow = envSec.locator('li[data-env-new="true"]')
    await newRow.locator('input[data-field="env-name"]').fill('CACHE_TTL')
    await newRow.locator('input[data-field="env-value"]').fill('60')

    await envSec.locator('[data-action="save-env"]').click()
    await expect(envSec.getByTestId('demo-env-saved')).toBeVisible()
    await expect(envSec.locator('li[data-env-name="CACHE_TTL"] input[data-field="env-value"]')).toHaveValue('60')
    expect(await smallText(page)).toEqual([])

    // Verify backend persisted plain changes and kept Secret-backed entry intact at resourceVersion 2.
    const verifyRes = await request.get('/api/demo/workloads/env?id=apps/Deployment/shop/worker')
    expect(verifyRes.status()).toBe(200)
    const verifyJson = (await verifyRes.json()) as {
      resourceVersion: string
      containers: { name: string; env: { name: string; value?: string; readOnlySource?: string }[] }[]
    }
    expect(verifyJson.resourceVersion).toBe('2')
    expect(verifyJson.containers[0]?.env).toEqual([
      { name: 'LOG_LEVEL', value: 'debug' },
      { name: 'API_SECRET', readOnlySource: 'secret:worker-secrets/api-key' },
      { name: 'CACHE_TTL', value: '60' },
    ])

    // Simulate concurrent external update advancing resourceVersion 2 -> 3 while UI holds version 2.
    await logInput.fill('trace')
    const concurrentRes = await request.patch('/api/demo/workloads/env', {
      headers: { Origin: origin },
      data: {
        id: 'apps/Deployment/shop/worker',
        resourceVersion: '2',
        container: 'worker',
        env: [
          { name: 'LOG_LEVEL', value: 'warn' },
          { name: 'CACHE_TTL', value: '120' },
        ],
      },
    })
    expect(concurrentRes.status()).toBe(200)

    // Saving stale resourceVersion 2 shows the 409 conflict notice; Reload recovers version 3.
    await envSec.locator('[data-action="save-env"]').click()
    const conflictBanner = envSec.getByTestId('demo-env-conflict')
    await expect(conflictBanner).toBeVisible()
    await expect(conflictBanner).toContainText('changed since it was loaded')
    expect(await smallText(page)).toEqual([])

    await conflictBanner.locator('[data-action="reload-env"]').click()
    await expect(conflictBanner).toHaveCount(0)
    await expect(logInput).toHaveValue('warn')
    await expect(envSec.locator('li[data-env-name="CACHE_TTL"] input[data-field="env-value"]')).toHaveValue('120')

    // Invalid variable name shows an inline error (>= 12px); removing CACHE_TTL and saving persists removal.
    await envSec.locator('[data-action="add-env"]').click()
    await envSec.locator('li[data-env-new="true"] input[data-field="env-name"]').fill('1INVALID')
    await envSec.locator('[data-action="save-env"]').click()
    await expect(envSec.getByTestId('demo-env-error')).toBeVisible()
    expect(await smallText(page)).toEqual([])
    await envSec.locator('li[data-env-new="true"] [data-action="remove-env"]').click()

    await envSec.locator('li[data-env-name="CACHE_TTL"] [data-action="remove-env"]').click()
    await envSec.locator('[data-action="save-env"]').click()
    await expect(envSec.getByTestId('demo-env-saved')).toBeVisible()
    await expect(envSec.locator('li[data-env-name="CACHE_TTL"]')).toHaveCount(0)

    const afterRemove = await request.get('/api/demo/workloads/env?id=apps/Deployment/shop/worker')
    const afterRemoveJson = (await afterRemove.json()) as {
      resourceVersion: string
      containers: { name: string; env: { name: string; value?: string; readOnlySource?: string }[] }[]
    }
    expect(afterRemoveJson.resourceVersion).toBe('4')
    expect(afterRemoveJson.containers[0]?.env).toEqual([
      { name: 'LOG_LEVEL', value: 'warn' },
      { name: 'API_SECRET', readOnlySource: 'secret:worker-secrets/api-key' },
    ])

    expect(foreignRequests(urls, origin)).toEqual([])
    expect(cspBlocked).toEqual([])
  })
})
