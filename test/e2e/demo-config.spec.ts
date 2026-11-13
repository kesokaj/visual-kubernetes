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

test.describe('demo cluster config editors off by default', () => {
  test.use({ baseURL: urlOf('telemetry') })

  test('hides config editors and refuses service-type, scale, and autoscaler APIs with 403', async ({
    page,
    request,
  }) => {
    for (const url of [
      '/api/demo/services/type?id=core/Service/shop/web',
      '/api/demo/workloads/scale?id=apps/Deployment/shop/web',
      '/api/demo/autoscalers?id=autoscaling/HorizontalPodAutoscaler/shop/web',
    ]) {
      const res = await request.get(url)
      expect(res.status()).toBe(403)
      expect(await res.text()).toContain('demo mode is off on this install')
    }

    await page.goto('/')
    await expect(web(page).locator(`[data-layer="bodies"] > g[data-id="${WEB_APP}"]`)).toHaveCount(1)
    await clickBody(page, WEB_APP)
    await expect(panel(page)).toHaveAttribute('data-panel-for', WEB_APP)
    await expect(panel(page).locator('[data-section="demo-service-type"]')).toHaveCount(0)
    await expect(panel(page).locator('[data-section="demo-scale"]')).toHaveCount(0)
    await expect(panel(page).locator('[data-section="demo-autoscaler"]')).toHaveCount(0)
  })
})

test.describe('interactive demo config editors (--demo)', () => {
  test.use({ baseURL: urlOf('demo') })

  test('switches Service exposure, revokes proxy on ClusterIP revert, enforces protections and 409 reload', async ({
    page,
    request,
  }) => {
    const origin = urlOf('demo')
    const urls = recordRequests(page)
    const cspBlocked = await recordCspViolations(page)

    // Protected Services & workloads are refused server-side with 403; DaemonSet scale returns 400.
    expect((await request.get('/api/demo/services/type?id=core/Service/default/kubernetes')).status()).toBe(403)
    expect((await request.get('/api/demo/services/type?id=core/Service/kube-system/kube-dns')).status()).toBe(403)
    expect((await request.get('/api/demo/workloads/scale?id=apps/Deployment/kube-system/coredns')).status()).toBe(403)
    expect((await request.get('/api/demo/workloads/scale?id=apps/DaemonSet/shop/agent')).status()).toBe(400)
    expect(
      (await request.get('/api/demo/autoscalers?id=autoscaling/HorizontalPodAutoscaler/kube-system/sys-hpa')).status(),
    ).toBe(403)

    await page.goto('/')
    await expect(web(page).locator('[data-layer="bodies"] > g[data-id="app/shop/Deployment/worker"]')).toHaveCount(1)

    try {
      // default/kubernetes and kube-system show explicit read-only notices.
      await clickBody(page, 'core/Service/default/kubernetes')
      await expect(panel(page).locator('[data-section="demo-service-type"]').getByTestId('service-type-readonly')).toContainText(
        'default/kubernetes API service cannot be modified',
      )

      await clickBody(page, 'app/kube-system/Deployment/coredns')
      await expect(panel(page).locator('[data-section="demo-service-type"]').getByTestId('service-type-readonly')).toContainText(
        'System namespace Services are read-only in demo mode.',
      )
      await expect(panel(page).locator('[data-section="demo-scale"]').getByTestId('scale-readonly')).toContainText(
        'System namespace workloads are read-only in demo mode.',
      )

      // Switch shop/worker Service ClusterIP -> LoadBalancer, verify demo-proxy reveals it and BrowserSheet opens.
      await clickBody(page, 'app/shop/Deployment/worker')
      const svcSec = panel(page).locator('[data-section="demo-service-type"]')
      const svcCard = svcSec.locator('[data-service-card="core/Service/shop/worker"]')
      await expect(svcCard.getByTestId('service-current-type-worker')).toHaveText('ClusterIP')
      await expect(panel(page).locator('[data-section="demo-proxy"]')).toHaveCount(0)

      await svcCard.getByTestId('service-type-option-LoadBalancer').click()
      await svcCard.getByTestId('service-type-save').click()
      await expect(svcCard.getByTestId('service-type-saved')).toBeVisible()
      await expect(svcCard.getByTestId('service-current-type-worker')).toHaveText('LoadBalancer')
      expect(await smallText(page)).toEqual([])

      const workerPortRow = panel(page).locator('[data-section="demo-proxy"] li[data-demo-port="shop/worker:80"]')
      await expect(workerPortRow).toHaveCount(1)
      await workerPortRow.locator('[data-action="open-port"]').click()

      const sheet = page.getByTestId('demo-browser')
      await expect(sheet).toBeVisible()
      const proxySrc = await page.getByTestId('demo-iframe').getAttribute('src')
      expect(proxySrc).toMatch(/^\/api\/demo\/proxy\/t\/[0-9a-f]{32}\/$/)

      // Reverting to ClusterIP immediately closes BrowserSheet, clears demo-proxy, and returns 403 on the old token.
      await svcCard.getByTestId('service-type-option-ClusterIP').click()
      await svcCard.getByTestId('service-type-save').click()
      await expect(svcCard.getByTestId('service-current-type-worker')).toHaveText('ClusterIP')
      await expect(sheet).toHaveCount(0)
      await expect(panel(page).locator('[data-section="demo-proxy"]')).toHaveCount(0)
      expect((await request.get(proxySrc ?? '/api/demo/proxy/t/revoked/')).status()).toBe(403)

      // Optimistic concurrency 409 + Reload on ServiceTypeSection while keeping shop/worker ClusterIP.
      const beforeConflict = await request.get('/api/demo/services/type?id=core/Service/shop/worker')
      const beforeJson = (await beforeConflict.json()) as { resourceVersion: string }
      await svcCard.getByTestId('service-type-option-LoadBalancer').click()
      const oobSvcPatch = await request.patch('/api/demo/services/type', {
        headers: { Origin: origin },
        data: {
          id: 'core/Service/shop/worker',
          resourceVersion: beforeJson.resourceVersion,
          type: 'ClusterIP',
        },
      })
      expect(oobSvcPatch.status()).toBe(200)

      await svcCard.getByTestId('service-type-save').click()
      await expect(svcCard.getByTestId('service-type-conflict')).toBeVisible()
      expect(await smallText(page)).toEqual([])
      await svcCard.getByTestId('service-type-reload').click()
      await expect(svcCard.getByTestId('service-type-conflict')).toHaveCount(0)
      await expect(svcCard.getByTestId('service-current-type-worker')).toHaveText('ClusterIP')
    } finally {
      const cur = await request.get('/api/demo/services/type?id=core/Service/shop/worker')
      if (cur.ok()) {
        const j = (await cur.json()) as { resourceVersion: string; type: string }
        if (j.type !== 'ClusterIP') {
          await request.patch('/api/demo/services/type', {
            headers: { Origin: origin },
            data: { id: 'core/Service/shop/worker', resourceVersion: j.resourceVersion, type: 'ClusterIP' },
          })
        }
      }
    }

    expect(foreignRequests(urls, origin)).toEqual([])
    expect(cspBlocked).toEqual([])
  })

  test('scales Deployment/StatefulSet with HPA warning, edits HPA & VPA, handles 409 conflicts, and passes R9/R10', async ({
    page,
    request,
  }) => {
    const origin = urlOf('demo')
    const urls = recordRequests(page)
    const cspBlocked = await recordCspViolations(page)

    const dbInit = (await (await request.get('/api/demo/workloads/scale?id=apps/StatefulSet/shop/db')).json()) as {
      replicas: number
    }
    const webInit = (await (await request.get('/api/demo/workloads/scale?id=apps/Deployment/shop/web')).json()) as {
      replicas: number
    }
    const hpaInit = (await (await request.get('/api/demo/autoscalers?id=autoscaling/HorizontalPodAutoscaler/shop/web')).json()) as {
      hpa: { minReplicas: number; maxReplicas: number }
    }
    const vpaInit = (
      await (await request.get('/api/demo/autoscalers?id=autoscaling.k8s.io/VerticalPodAutoscaler/shop/worker')).json()
    ) as {
      vpa: { updateMode: string; containers: { containerName: string; maxCPU?: string; maxMemory?: string }[] }
    }

    try {
      await page.goto('/')
      await expect(web(page).locator(`[data-layer="bodies"] > g[data-id="${WEB_APP}"]`)).toHaveCount(1)

      // DaemonSet explains why replica scaling is read-only.
      await clickBody(page, 'app/shop/DaemonSet/agent')
      await expect(panel(page).locator('[data-section="demo-scale"]').getByTestId('scale-readonly')).toContainText(
        '1 pod per matching node',
      )

      // StatefulSet shop/db scales 1 -> 3 and restores to 1.
      await clickBody(page, 'app/shop/StatefulSet/db')
      const dbScale = panel(page).locator('[data-section="demo-scale"]')
      await expect(dbScale.getByTestId('scale-input')).toHaveValue('1')
      await dbScale.getByTestId('scale-input').fill('3')
      await dbScale.getByTestId('scale-save').click()
      await expect(dbScale.getByTestId('scale-saved')).toBeVisible()
      await dbScale.getByTestId('scale-input').fill('1')
      await dbScale.getByTestId('scale-save').click()
      await expect(dbScale.getByTestId('scale-saved')).toBeVisible()

      // Deployment shop/web shows inline HPA warning, scales 2 -> 4, hits 409 conflict, shows validation on invalid draft, reloads, restores 2.
      await clickBody(page, WEB_APP)
      const webScale = panel(page).locator('[data-section="demo-scale"]')
      await expect(webScale.getByTestId('scale-hpa-warning')).toContainText('HPA “web” actively manages replicas here')
      await expect(webScale.getByTestId('scale-input')).toHaveValue('2')
      await webScale.getByTestId('scale-inc').click()
      await webScale.getByTestId('scale-inc').click()
      await webScale.getByTestId('scale-save').click()
      await expect(webScale.getByTestId('scale-saved')).toBeVisible()

      const scaledRes = await request.get('/api/demo/workloads/scale?id=apps/Deployment/shop/web')
      const scaledJson = (await scaledRes.json()) as { resourceVersion: string; replicas: number; managedByHPA: string }
      expect(scaledJson.replicas).toBe(4)
      expect(scaledJson.managedByHPA).toBe('web')

      await webScale.getByTestId('scale-input').fill('5')
      const oobScale = await request.patch('/api/demo/workloads/scale', {
        headers: { Origin: origin },
        data: { id: 'apps/Deployment/shop/web', resourceVersion: scaledJson.resourceVersion, replicas: 3 },
      })
      expect(oobScale.status()).toBe(200)
      await webScale.getByTestId('scale-save').click()
      await expect(webScale.getByTestId('scale-conflict')).toBeVisible()
      await webScale.getByTestId('scale-input').fill('-1')
      await expect(webScale.getByTestId('scale-error')).toContainText('between 0 and 50')
      await expect(webScale.getByTestId('scale-save')).toBeDisabled()
      expect(await smallText(page)).toEqual([])
      await webScale.getByTestId('scale-reload').click()
      await expect(webScale.getByTestId('scale-input')).toHaveValue('3')
      await webScale.getByTestId('scale-input').fill('2')
      await webScale.getByTestId('scale-save').click()
      await expect(webScale.getByTestId('scale-saved')).toBeVisible()

      // HPA satellite editing + 409 conflict + invalid bounds visibility + reload.
      await clickSatellite(page, 'autoscaling/HorizontalPodAutoscaler/shop/web')
      const hpaCard = panel(page).locator('[data-autoscaler-card="autoscaling/HorizontalPodAutoscaler/shop/web"]')
      await expect(hpaCard.getByTestId('hpa-current-replicas')).toContainText('current replicas: 2')
      await hpaCard.getByTestId('hpa-min-input').fill('2')
      await hpaCard.getByTestId('hpa-max-input').fill('8')
      await hpaCard.getByTestId('autoscaler-save').click()
      await expect(hpaCard.getByTestId('autoscaler-saved')).toBeVisible()

      const hpaAfter = await request.get('/api/demo/autoscalers?id=autoscaling/HorizontalPodAutoscaler/shop/web')
      const hpaJson = (await hpaAfter.json()) as {
        resourceVersion: string
        hpa: { minReplicas: number; maxReplicas: number }
      }
      expect(hpaJson.hpa.minReplicas).toBe(2)
      expect(hpaJson.hpa.maxReplicas).toBe(8)

      await hpaCard.getByTestId('hpa-max-input').fill('9')
      const oobHpa = await request.patch('/api/demo/autoscalers', {
        headers: { Origin: origin },
        data: {
          id: 'autoscaling/HorizontalPodAutoscaler/shop/web',
          resourceVersion: hpaJson.resourceVersion,
          hpa: { minReplicas: 2, maxReplicas: 6 },
        },
      })
      expect(oobHpa.status()).toBe(200)
      await hpaCard.getByTestId('autoscaler-save').click()
      await expect(hpaCard.getByTestId('autoscaler-conflict')).toBeVisible()
      await hpaCard.getByTestId('hpa-min-input').fill('10')
      await expect(hpaCard.getByTestId('autoscaler-error')).toContainText('Min replicas cannot exceed Max replicas')
      await expect(hpaCard.getByTestId('autoscaler-save')).toBeDisabled()
      await hpaCard.getByTestId('autoscaler-reload').click()
      await expect(hpaCard.getByTestId('hpa-max-input')).toHaveValue('6')

      // VPA satellite editing: explicit vertical scaling note, updateMode, maxCPU, empty maxMemory.
      await clickSatellite(page, 'autoscaling.k8s.io/VerticalPodAutoscaler/shop/worker')
      const vpaCard = panel(page).locator('[data-autoscaler-card="autoscaling.k8s.io/VerticalPodAutoscaler/shop/worker"]')
      await expect(vpaCard.getByTestId('vpa-vertical-note')).toContainText('resizes container CPU and memory vertically')
      await vpaCard.getByTestId('vpa-mode-select').selectOption('Recreate')
      await vpaCard.getByTestId('vpa-max-cpu-worker').fill('1')
      await vpaCard.getByTestId('vpa-max-memory-worker').fill('')
      await vpaCard.getByTestId('autoscaler-save').click()
      await expect(vpaCard.getByTestId('autoscaler-saved')).toBeVisible()

      const vpaAfter = await request.get(
        '/api/demo/autoscalers?id=autoscaling.k8s.io/VerticalPodAutoscaler/shop/worker',
      )
      const vpaJson = (await vpaAfter.json()) as {
        vpa: { updateMode: string; containers: { containerName: string; maxCPU?: string; maxMemory?: string }[] }
      }
      expect(vpaJson.vpa.updateMode).toBe('Recreate')
      expect(vpaJson.vpa.containers).toEqual([{ containerName: 'worker', maxCPU: '1', maxMemory: '' }])

      // R9 & R10 clean pass + must-fail validation inside the config panel.
      expect(await smallText(page)).toEqual([])
      expect(foreignRequests(urls, origin)).toEqual([])
      expect(cspBlocked).toEqual([])
      expect(foreignRequests(['https://203.0.113.9/leak.js'], origin)).toEqual(['https://203.0.113.9/leak.js'])

      await page.evaluate(() => {
        const tiny = document.createElement('span')
        tiny.textContent = 'tiny-config-label'
        tiny.style.fontSize = '10px'
        document.querySelector('[data-testid="panel"]')?.append(tiny)
      })
      expect((await smallText(page)).map((s) => s.text)).toContain('tiny-config-label')
    } finally {
      for (const [id, replicas] of [
        ['apps/StatefulSet/shop/db', dbInit.replicas],
        ['apps/Deployment/shop/web', webInit.replicas],
      ] as const) {
        const cur = await request.get(`/api/demo/workloads/scale?id=${encodeURIComponent(id)}`)
        if (cur.ok()) {
          const j = (await cur.json()) as { resourceVersion: string; replicas: number }
          if (j.replicas !== replicas) {
            await request.patch('/api/demo/workloads/scale', {
              headers: { Origin: origin },
              data: { id, resourceVersion: j.resourceVersion, replicas },
            })
          }
        }
      }
      const curHpa = await request.get('/api/demo/autoscalers?id=autoscaling/HorizontalPodAutoscaler/shop/web')
      if (curHpa.ok()) {
        const j = (await curHpa.json()) as { resourceVersion: string }
        await request.patch('/api/demo/autoscalers', {
          headers: { Origin: origin },
          data: { id: 'autoscaling/HorizontalPodAutoscaler/shop/web', resourceVersion: j.resourceVersion, hpa: hpaInit.hpa },
        })
      }
      const curVpa = await request.get(
        '/api/demo/autoscalers?id=autoscaling.k8s.io/VerticalPodAutoscaler/shop/worker',
      )
      if (curVpa.ok()) {
        const j = (await curVpa.json()) as { resourceVersion: string }
        await request.patch('/api/demo/autoscalers', {
          headers: { Origin: origin },
          data: {
            id: 'autoscaling.k8s.io/VerticalPodAutoscaler/shop/worker',
            resourceVersion: j.resourceVersion,
            vpa: vpaInit.vpa,
          },
        })
      }
    }
  })
})
