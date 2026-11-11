import { expect, test, type Page } from '@playwright/test'
import { foreignRequests, recordRequests, smallText } from './checks'
import { urlOf } from './servers'

// The click panel (plan Phase 4): config YAML from /api/objects, live Events, metrics
// sparklines, pod logs from /api/logs, node conditions, the capability list on the API
// server; Esc, × and a click on empty space close it. Runs on the telemetry stream, which
// repeats metrics and Events for ten minutes, and on missing-sources for capabilities.

const web = (page: Page) => page.getByTestId('web')
const panel = (page: Page) => page.getByTestId('panel')
const WEB = 'app/shop/Deployment/web'

// clickBody clicks an anchor's own icon: the first <use> directly under its group.
async function clickBody(page: Page, id: string) {
  await web(page).locator(`[data-layer="bodies"] > g[data-id="${id}"] > use`).first().click()
}

async function ready(page: Page) {
  await page.goto('/')
  await expect(web(page).locator(`[data-layer="bodies"] > g[data-id="${WEB}"]`)).toHaveCount(1)
  // wait until the layout has settled enough for a click to land on what it aims at
  await expect(web(page)).toHaveAttribute('data-size-by', 'network', { timeout: 10_000 })
}

test.describe('telemetry', () => {
  test.use({ baseURL: urlOf('telemetry') })

  test('an app panel shows summary, metrics, pods, Events, logs and config', async ({ page }) => {
    const urls = recordRequests(page)
    await ready(page)
    await clickBody(page, WEB)
    const p = panel(page)
    await expect(p).toHaveAttribute('data-panel-for', WEB)
    await expect(p).toHaveAttribute('data-panel-role', 'app')
    await expect(web(page).locator(`g[data-id="${WEB}"]`)).toHaveAttribute('data-selected', 'true')
    // metrics: one sparkline per measure the samples carry, filling as samples arrive
    const net = p.locator('[data-section="metrics"] [data-metric="network"]')
    await expect(net.locator('polyline')).toHaveCount(1)
    await expect(p.locator('[data-section="metrics"] [data-metric="cpu"]')).toHaveCount(1)
    await expect.poll(async () => (await net.locator('polyline').getAttribute('points'))?.split(' ').length ?? 0, { timeout: 10_000 }).toBeGreaterThan(2)
    // the pods, as chips
    await expect(p.locator('[data-section="pods"] button.chip')).toHaveCount(3)
    // Events about the Deployment and its pods, never about another app's
    const events = p.locator('[data-section="events"]')
    await expect(events.locator('li[data-reason="ScalingReplicaSet"]')).toHaveCount(1, { timeout: 10_000 })
    await expect(events.locator('li[data-reason="Scheduled"]')).toHaveCount(1)
    await expect(events.locator('li[data-reason="BackOff"]')).toHaveCount(0)
    // logs of the first pod, with the secrets warning; replay mode records no logs
    await expect(p.locator('[data-section="logs"]')).toContainText('logs can contain secrets')
    await expect(p.locator('[data-section="logs"] pre.logs')).toContainText('replay mode: logs are not recorded')
    // config of the app's top workload from /api/objects; replay serves only entities in the
    // view and the Deployment itself is not one, so here it is an honest 404, never a blank
    await expect(p.locator('[data-section="config"]')).toContainText('apps/Deployment/shop/web')
    await expect(p.locator('[data-section="config"] .err')).toContainText('404')
    // R9 and R10 hold with the panel open
    expect(await smallText(page)).toEqual([])
    expect(urls.some((u) => u.includes('/api/objects?'))).toBe(true)
    expect(urls.some((u) => u.includes('/api/logs?'))).toBe(true)
    expect(foreignRequests(urls, new URL(page.url()).origin)).toEqual([])
  })

  test('a pod chip opens the pod; a Warning Event shows on its app', async ({ page }) => {
    await ready(page)
    await clickBody(page, 'app/shop/Deployment/api')
    const p = panel(page)
    await expect(p.locator('[data-section="events"] li[data-reason="BackOff"]')).toHaveClass(/warn/, { timeout: 10_000 })
    await p.locator('button.chip[data-pod="core/Pod/shop/api-x1"]').click()
    await expect(p).toHaveAttribute('data-panel-for', 'core/Pod/shop/api-x1')
    await expect(p).toHaveAttribute('data-panel-role', 'pod')
    await expect(p.locator('[data-section="summary"]')).toContainText('node-2')
    await expect(p.locator('[data-section="events"] li[data-reason="BackOff"]')).toHaveCount(1)
    await expect(p.locator('[data-section="logs"] pre.logs')).toContainText('replay mode')
    await expect(p.locator('[data-section="metrics"] [data-metric="network"]')).toHaveCount(1)
    // the pod's own config, as /api/objects serves it
    await expect(p.locator('[data-section="config"] pre.yaml')).toContainText('name: api-x1')
    await expect(p.locator('[data-section="config"] pre.yaml')).toContainText('nodeName: node-2')
  })

  test('Esc, the close button and a click on empty space each close the panel', async ({ page }) => {
    await ready(page)
    await clickBody(page, WEB)
    await expect(panel(page)).toHaveCount(1)
    await page.keyboard.press('Escape')
    await expect(panel(page)).toHaveCount(0)
    await expect(web(page).locator('[data-selected]')).toHaveCount(0)
    await clickBody(page, WEB)
    await page.getByRole('button', { name: 'Close panel' }).click()
    await expect(panel(page)).toHaveCount(0)
    await clickBody(page, WEB)
    await expect(panel(page)).toHaveCount(1)
    await page.mouse.click(8, 450)
    await expect(panel(page)).toHaveCount(0)
  })

  test('a node panel lists its conditions from the Node YAML', async ({ page }) => {
    const node = 'core/Node/_/node-2'
    await page.route(
      (u) => u.pathname === '/api/objects' && u.searchParams.get('id') === node,
      (route) =>
        route.fulfill({
          contentType: 'application/yaml',
          body: [
            'kind: Node',
            'metadata:',
            '  name: node-2',
            'status:',
            '  conditions:',
            '  - type: MemoryPressure',
            '    status: "False"',
            '    reason: KubeletHasSufficientMemory',
            '  - type: Ready',
            '    status: "True"',
            '    reason: KubeletReady',
            '',
          ].join('\n'),
        }),
    )
    await ready(page)
    await clickBody(page, node)
    const p = panel(page)
    await expect(p).toHaveAttribute('data-panel-role', 'node')
    await expect(p.locator('li[data-condition="Ready"]')).toHaveAttribute('data-status', 'True')
    await expect(p.locator('li[data-condition="MemoryPressure"]')).toHaveAttribute('data-status', 'False')
    await expect(p.locator('li[data-condition="MemoryPressure"]')).not.toHaveClass(/warn/)
    await expect(p.locator('[data-section="events"] li[data-reason="NodeReady"]')).toHaveCount(1, { timeout: 10_000 })
    await expect(p.locator('[data-section="config"] pre.yaml')).toContainText('KubeletReady')
  })

  test('without conditions in the YAML a node panel falls back to its status', async ({ page }) => {
    await ready(page)
    await clickBody(page, 'core/Node/_/node-1')
    const p = panel(page)
    await expect(p.locator('[data-section="config"] pre.yaml')).toBeVisible()
    await expect(p.locator('[data-fallback="status"]')).toContainText('Ready')
    await expect(p.locator('li[data-condition]')).toHaveCount(0)
  })

  test('a bare Pod app opens as an app, with its pod logs (BUG-001)', async ({ page }) => {
    const urls = recordRequests(page)
    await ready(page)
    await clickBody(page, 'app/kube-system/Pod/kube-scheduler-node-1')
    const p = panel(page)
    await expect(p).toHaveAttribute('data-panel-role', 'app')
    await expect(p.locator('button.chip[data-pod="core/Pod/kube-system/kube-scheduler-node-1"]')).toHaveCount(1)
    await expect(p.locator('[data-section="logs"] pre.logs')).toContainText('replay mode')
    expect(urls.filter((u) => /[?&]id=app%2F/.test(u))).toEqual([])
  })

  test('a cp/* part names its evidence and the static pods folded behind it', async ({ page }) => {
    await ready(page)
    await clickBody(page, 'cp/etcd')
    const p = panel(page)
    await expect(p).toHaveAttribute('data-panel-role', 'cp')
    await expect(p.locator('[data-section="summary"]')).toContainText('lease')
    await expect(p.locator('[data-static="app/kube-system/Pod/etcd-node-1"]')).toHaveCount(1)
    await expect(p.locator('[data-section="config"]')).toHaveCount(0)
  })
})

test.describe('missing sources', () => {
  test.use({ baseURL: urlOf('missingSources') })

  test('the API server panel lists every data source and its state', async ({ page }) => {
    await page.goto('/')
    await clickBody(page, 'cp/apiserver')
    const p = panel(page)
    await expect(p).toHaveAttribute('data-panel-role', 'hub')
    await expect(p.locator('li[data-cap="metrics.k8s.io"]')).toHaveAttribute('data-state', 'missing')
    await expect(p.locator('li[data-cap="kubelet"]')).toHaveAttribute('data-state', 'unreachable')
    await expect(p.locator('li[data-cap="apiserver-metrics"]')).toHaveAttribute('data-state', 'denied')
    await expect(p.locator('li[data-cap="discovery"]')).toHaveAttribute('data-state', 'ok')
    expect(await smallText(page)).toEqual([])
  })
})

// The recorded deploy-scale stream carries the Kubernetes Events the backend emits (D29):
// its own server, so this spec is the first subscriber and sees them replayed live.
test.describe('replayed Events', () => {
  test.use({ baseURL: urlOf('deployScaleEvents') })

  test('Events recorded in a stream reach the app and pod panels', async ({ page }) => {
    await page.goto('/')
    await expect(web(page).locator(`[data-layer="bodies"] > g[data-id="${WEB}"]`)).toHaveCount(1)
    await clickBody(page, WEB)
    const p = panel(page)
    await expect(p).toHaveAttribute('data-panel-for', WEB)
    const events = p.locator('[data-section="events"]')
    // about the Deployment (the app's owner id), its ReplicaSet (the pods' owner id), its pods
    await expect(events.locator('li[data-reason="ScalingReplicaSet"]')).toHaveCount(1, { timeout: 10_000 })
    await expect(events.locator('li[data-reason="SuccessfulCreate"]')).toHaveCount(2, { timeout: 10_000 })
    await expect(events.locator('li[data-reason="Scheduled"]')).toHaveCount(2, { timeout: 10_000 })
    await expect(events).toContainText('Scaled up replica set web-7d4b9c from 2 to 4')
    // a pod panel shows only that pod's Event
    await p.locator('button.chip[data-pod="core/Pod/shop/web-7d4b9c-x002"]').click()
    await expect(p).toHaveAttribute('data-panel-for', 'core/Pod/shop/web-7d4b9c-x002')
    await expect(events.locator('li[data-reason="Scheduled"]')).toHaveCount(1)
    await expect(events).toContainText('Successfully assigned shop/web-7d4b9c-x002 to node-2')
    await expect(events.locator('li[data-reason="ScalingReplicaSet"]')).toHaveCount(0)
    expect(await smallText(page)).toEqual([])
  })
})

test.describe('logs disabled (--logs=false)', () => {
  test.use({ baseURL: urlOf('logsOff') })

  test('/api/logs answers 403 and the Logs section says logs are off on this install', async ({ page }) => {
    const logStatuses: number[] = []
    page.on('response', (res) => {
      if (res.url().includes('/api/logs?')) logStatuses.push(res.status())
    })
    await page.goto('/')
    await expect(web(page).locator(`[data-layer="bodies"] > g[data-id="${WEB}"]`)).toHaveCount(1)
    await clickBody(page, WEB)
    const p = panel(page)
    const logs = p.locator('[data-section="logs"]')
    await expect(logs.locator('[data-logs="off"]')).toContainText('logs are off on this install')
    await expect(logs.locator('pre.logs')).toHaveCount(0)
    await expect(logs.locator('.err')).toHaveCount(0)
    expect(logStatuses).toContain(403)

    await p.locator('button.chip[data-pod="core/Pod/shop/web-7d4b9c-x001"]').click()
    await expect(p).toHaveAttribute('data-panel-role', 'pod')
    await expect(logs.locator('[data-logs="off"]')).toContainText('logs are off on this install')
    await expect(p.locator('[data-section="config"] pre.yaml')).toContainText('name: web-7d4b9c-x001')
    expect(await smallText(page)).toEqual([])
  })
})
