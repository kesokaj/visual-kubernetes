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
  await expect(page.locator('.f-row')).toHaveCount(9)
  expect(await smallText(page)).toEqual([])
  // zoomed far out, labels keep their size (they live outside the zoomed world)
  await page.mouse.move(800, 450)
  for (let i = 0; i < 6; i++) await page.mouse.wheel(0, 800)
  await expect(web(page)).toHaveAttribute('data-zoom', '0.250')
  expect(await smallText(page)).toEqual([])
})

test('grid paper sits on a white page behind the world and follows pan and zoom', async ({ page }) => {
  await page.goto('/')
  const svg = web(page)
  await expect(svg.locator('[data-role="node"]')).toHaveCount(3)
  const bg = await page.locator('.vk').evaluate((el) => getComputedStyle(el).backgroundColor)
  expect(bg).toBe('rgb(255, 255, 255)')

  const grid = svg.getByTestId('grid-paper')
  await expect(grid).toHaveCount(1)
  await expect(grid).toHaveAttribute('fill', 'url(#vk-grid)')
  const behindWorld = await svg.evaluate((el) => {
    const g = el.querySelector('[data-testid="grid-paper"]')
    const w = el.querySelector('[data-layer="world"]')
    return Boolean(g && w && (g.compareDocumentPosition(w) & Node.DOCUMENT_POSITION_FOLLOWING) !== 0)
  })
  expect(behindWorld).toBe(true)

  const initialTransform = await svg.locator('#vk-grid').getAttribute('patternTransform')
  expect(initialTransform).toBeTruthy()
  await page.mouse.move(800, 450)
  await page.mouse.wheel(0, -500)
  await expect.poll(async () => svg.locator('#vk-grid').getAttribute('patternTransform')).not.toBe(initialTransform)
})

test('settled bubbles and screen-space labels do not touch or overlap on a populated cluster', async ({ page }) => {
  const ent = (id: string, kind: string, group: string, name: string, namespace: string, filterGroup: string, extra = {}) => ({
    id,
    kind,
    group,
    name,
    namespace,
    labels: {},
    status: '2/2 ready',
    filterGroup,
    ...extra,
  })
  const nodes = ['node-1', 'node-2', 'node-3']
  const cpParts = ['etcd', 'kube-scheduler', 'kube-controller-manager']
  const workloads: readonly [string, string, string, number][] = [
    ['kube-system', 'DaemonSet', 'cilium', 3],
    ['kube-system', 'DaemonSet', 'cilium-envoy', 3],
    ['kube-system', 'Deployment', 'cilium-operator', 1],
    ['kube-system', 'Deployment', 'coredns', 2],
    ['kube-system', 'Deployment', 'hubble-relay', 1],
    ['kube-system', 'DaemonSet', 'kube-proxy', 3],
    ['kube-system', 'Deployment', 'metrics-server', 1],
    ['local-path-storage', 'Deployment', 'local-path-provisioner', 1],
    ['vk-demo', 'Deployment', 'web', 2],
    ['vk-demo', 'Deployment', 'api', 2],
    ['vk-demo', 'Deployment', 'cache', 1],
  ]
  const entities: Record<string, unknown>[] = [
    ent('cp/apiserver', 'APIServer', 'cp', 'apiserver', '', 'controlPlane'),
    ...cpParts.map((c) => ent(`cp/${c}`, 'ControllerManager', 'cp', c, '', 'controlPlane')),
    ...nodes.map((n) => ent(`core/Node/_/${n}`, 'Node', 'core', n, '', 'nodes')),
  ]
  const edges: Record<string, string>[] = [
    ...cpParts.map((c) => ({ id: `member:cp/apiserver->cp/${c}`, from: 'cp/apiserver', to: `cp/${c}`, kind: 'member' })),
    ...nodes.map((n) => ({ id: `member:cp/apiserver->core/Node/_/${n}`, from: 'cp/apiserver', to: `core/Node/_/${n}`, kind: 'member' })),
  ]
  for (const [ns, kind, name, replicas] of workloads) {
    const appId = `app/${ns}/${kind}/${name}`
    entities.push(ent(appId, kind, 'apps', name, ns, ns === 'vk-demo' ? 'workloads' : 'system'))
    for (let i = 0; i < replicas; i++) {
      const node = nodes[i % nodes.length]!
      entities.push(
        ent(`core/Pod/${ns}/${name}-${i}`, 'Pod', 'core', `${name}-${i}`, ns, 'pods', { appId, nodeName: node }),
      )
      edges.push({ id: `hosts:core/Node/_/${node}->${appId}`, from: `core/Node/_/${node}`, to: appId, kind: 'hosts' })
    }
  }
  await page.route('**/api/stream', (route) =>
    route.fulfill({
      status: 200,
      headers: { 'content-type': 'text/event-stream', 'cache-control': 'no-cache' },
      body: `retry: 60000\nevent: snapshot\ndata: ${JSON.stringify({ entities, edges, capabilities: {} })}\n\n`,
    }),
  )

  await page.goto('/')
  await expect(web(page).locator('[data-layer="bodies"] > g[data-id]')).toHaveCount(18)
  // Let d3-force and auto-fit settle for 3.5 s before checking pairwise clearances.
  await page.waitForTimeout(3500)

  const checkCollisions = () =>
    page.evaluate(() => {
      type Circle = { id: string; cx: number; cy: number; r: number }
      type Rect = { id: string; left: number; top: number; right: number; bottom: number }
      const circles: Circle[] = []
      for (const g of document.querySelectorAll<SVGGElement>('[data-layer="bodies"] > g[data-id]')) {
        const id = g.dataset.id ?? ''
        const ring = g.querySelector<SVGGraphicsElement>('circle.ring')
        const icon = g.querySelector<SVGGraphicsElement>('use')
        const shape = ring ?? icon
        if (!shape) continue
        const b = shape.getBoundingClientRect()
        // Include pod-dot outer radius beyond circle.ring (14 world px * k).
        const pod = g.querySelector<SVGGraphicsElement>('[data-role="pod"] circle')
        const podR = pod ? pod.getBoundingClientRect().width / 2 : 0
        circles.push({ id, cx: b.left + b.width / 2, cy: b.top + b.height / 2, r: Math.max(b.width, b.height) / 2 + podR })
      }
      const labels: Rect[] = []
      for (const g of document.querySelectorAll<SVGGElement>('[data-layer="labels"] > g[data-label-for]')) {
        const id = g.dataset.labelFor ?? ''
        const b = g.getBoundingClientRect()
        if (b.width > 0 && b.height > 0) {
          labels.push({ id, left: b.left, top: b.top, right: b.right, bottom: b.bottom })
        }
      }
      const touchingBodies: string[] = []
      for (let i = 0; i < circles.length; i++) {
        for (let j = i + 1; j < circles.length; j++) {
          const a = circles[i]!
          const b = circles[j]!
          const gap = Math.hypot(a.cx - b.cx, a.cy - b.cy) - (a.r + b.r)
          if (gap < 14) touchingBodies.push(`${a.id}<->${b.id}:${Math.round(gap)}`)
        }
      }
      const touchingLabels: string[] = []
      for (let i = 0; i < labels.length; i++) {
        for (let j = i + 1; j < labels.length; j++) {
          const a = labels[i]!
          const b = labels[j]!
          if (a.left < b.right && a.right > b.left && a.top < b.bottom && a.bottom > b.top) {
            touchingLabels.push(`${a.id}<->${b.id}`)
          }
        }
        const lbl = labels[i]!
        for (const c of circles) {
          if (c.id === lbl.id) continue
          const nx = Math.max(lbl.left, Math.min(c.cx, lbl.right))
          const ny = Math.max(lbl.top, Math.min(c.cy, lbl.bottom))
          if (Math.hypot(nx - c.cx, ny - c.cy) < c.r) {
            touchingLabels.push(`label:${lbl.id}<->body:${c.id}`)
          }
        }
      }
      return { bodies: circles.length, labels: labels.length, touchingBodies, touchingLabels }
    })

  const settled = await checkCollisions()
  expect(settled.touchingBodies).toEqual([])
  expect(settled.touchingLabels).toEqual([])

  // Must-fail check: placing a synthetic label over an existing label is caught immediately.
  await page.evaluate(() => {
    const first = document.querySelector<SVGGElement>('[data-layer="labels"] > g[data-label-for]')
    if (!first) return
    const dup = first.cloneNode(true) as SVGGElement
    dup.dataset.labelFor = 'synthetic-collision'
    first.parentElement?.append(dup)
  })
  const collided = await checkCollisions()
  expect(collided.touchingLabels.some((s) => s.includes('synthetic-collision'))).toBe(true)
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
