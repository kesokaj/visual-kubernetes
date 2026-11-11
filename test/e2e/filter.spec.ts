import { expect, test, type Page } from '@playwright/test'
import { WEB_APP, smallText } from './checks'

// The filter pill (plan D3 + UI Task): eight component groups (all on by default), visual-kubernetes
// itself hidden by default, plus compact Deployment/StatefulSet/DaemonSet and Namespace filters in
// Highlight or Only-show mode; V hides the pill.

const GROUP_LABELS = [
  'Control plane',
  'Nodes',
  'Workloads',
  'Pods',
  'Services & config',
  'Traffic',
  'Internet & ingress',
  'System add-ons',
]

const PROXY_APP = 'app/kube-system/DaemonSet/kube-proxy'

async function openPill(page: Page) {
  await page.goto('/')
  await expect(page.getByTestId('web').locator('[data-role="node"]')).toHaveCount(3)
  await page.locator('.f-pill').click()
  await expect(page.getByRole('switch')).toHaveCount(9)
}

test('eight groups all on by default and visual-kubernetes hidden by default', async ({ page }) => {
  await openPill(page)
  await expect(page.locator('.f-pill')).toHaveText('Showing all')
  await expect(page.locator('[aria-label="Show component groups"] .f-row span')).toHaveText(GROUP_LABELS)
  for (const label of GROUP_LABELS) await expect(page.getByRole('switch', { name: label })).toBeChecked()
  await expect(page.getByRole('switch', { name: 'Show visual-kubernetes' })).not.toBeChecked()
  expect(await smallText(page)).toEqual([])
})

test('hiding a group removes it and re-fits the web', async ({ page }) => {
  await openPill(page)
  const svg = page.getByTestId('web')
  await expect(svg.locator(`[data-id="${WEB_APP}"] [data-role="pod"]`)).toHaveCount(4, { timeout: 15_000 })
  // hand the camera to the presenter first, so the re-fit is visible as auto turning back on
  await page.mouse.move(900, 300)
  await page.mouse.wheel(0, -400)
  await expect(svg).toHaveAttribute('data-auto', 'off')

  await page.getByRole('switch', { name: 'Pods' }).click()
  await expect(svg.locator('[data-role="pod"]')).toHaveCount(0)
  await expect(svg).toHaveAttribute('data-auto', 'on')
  await expect(page.locator('.f-pill')).toHaveText('7 of 8 shown')

  await page.getByRole('switch', { name: 'Nodes' }).click()
  await expect(svg.locator('[data-role="node"]')).toHaveCount(0)
  await expect(svg.locator('[data-kind="hosts"], [data-kind="member"]')).toHaveCount(0)
  await expect(page.locator('.f-pill')).toHaveText('6 of 8 shown')

  // a hidden workload takes its satellites with it
  await page.getByRole('switch', { name: 'Workloads' }).click()
  await expect(svg.locator(`[data-id="${WEB_APP}"]`)).toHaveCount(0)
  await expect(svg.locator('[data-role="sat"]')).toHaveCount(0)
  await expect(svg.locator('[data-role="hub"]')).toHaveCount(1)

  await page.getByRole('button', { name: 'All on' }).click()
  await expect(page.locator('.f-pill')).toHaveText('Showing all')
  await expect(svg.locator('[data-role="node"]')).toHaveCount(3)
  await expect(svg.locator(`[data-id="${WEB_APP}"] [data-role="pod"]`)).toHaveCount(4)
})

test('visual-kubernetes (own namespace and kube-system variant) is hidden by default and excluded from halos', async ({
  page,
}) => {
  const ent = (id: string, kind: string, group: string, name: string, namespace: string, filterGroup: string, extra = {}) => ({
    id,
    kind,
    group,
    name,
    namespace,
    labels: {},
    status: '1/1 ready',
    filterGroup,
    ...extra,
  })
  const snapshot = {
    entities: [
      ent('cp/apiserver', 'APIServer', 'cp', 'apiserver', '', 'controlPlane'),
      ent('core/Node/_/node-1', 'Node', 'core', 'node-1', '', 'nodes'),
      ent('app/visual-kubernetes/Deployment/visual-kubernetes', 'Deployment', 'apps', 'visual-kubernetes', 'visual-kubernetes', 'workloads', {
        labels: { 'app.kubernetes.io/name': 'visual-kubernetes' },
      }),
      ent('core/Pod/visual-kubernetes/visual-kubernetes-abc', 'Pod', 'core', 'visual-kubernetes-abc', 'visual-kubernetes', 'pods', {
        appId: 'app/visual-kubernetes/Deployment/visual-kubernetes',
        nodeName: 'node-1',
      }),
      ent('app/visual-kubernetes/Pod/visual-kubernetes-probe', 'Pod', 'core', 'visual-kubernetes-probe', 'visual-kubernetes', 'workloads'),
      ent('networking.k8s.io/NetworkPolicy/visual-kubernetes/visual-kubernetes-deny-ingress', 'NetworkPolicy', 'networking.k8s.io', 'visual-kubernetes-deny-ingress', 'visual-kubernetes', 'servicesConfig'),
      ent('app/kube-system/Deployment/visual-kubernetes', 'Deployment', 'apps', 'visual-kubernetes', 'kube-system', 'system', {
        labels: { 'app.kubernetes.io/name': 'visual-kubernetes' },
      }),
      ent('app/shop/Deployment/web', 'Deployment', 'apps', 'web', 'shop', 'workloads'),
      ent('core/Pod/shop/web-1', 'Pod', 'core', 'web-1', 'shop', 'pods', {
        appId: 'app/shop/Deployment/web',
        nodeName: 'node-1',
      }),
    ],
    edges: [
      { id: 'member:cp/apiserver->core/Node/_/node-1', from: 'cp/apiserver', to: 'core/Node/_/node-1', kind: 'member' },
      { id: 'hosts:core/Node/_/node-1->app/visual-kubernetes/Deployment/visual-kubernetes', from: 'core/Node/_/node-1', to: 'app/visual-kubernetes/Deployment/visual-kubernetes', kind: 'hosts' },
      { id: 'hosts:core/Node/_/node-1->app/shop/Deployment/web', from: 'core/Node/_/node-1', to: 'app/shop/Deployment/web', kind: 'hosts' },
    ],
    capabilities: {},
  }
  const metrics = {
    sizeBy: 'network',
    samples: [
      { id: 'app/visual-kubernetes/Deployment/visual-kubernetes', rxBps: 900_000, txBps: 300_000 },
      { id: 'app/kube-system/Deployment/visual-kubernetes', rxBps: 500_000, txBps: 100_000 },
      { id: 'app/shop/Deployment/web', rxBps: 40_000, txBps: 10_000 },
    ],
  }
  await page.route('**/api/stream', (route) =>
    route.fulfill({
      status: 200,
      headers: { 'content-type': 'text/event-stream', 'cache-control': 'no-cache' },
      body:
        `retry: 60000\nevent: snapshot\ndata: ${JSON.stringify(snapshot)}\n\n` +
        `event: metrics\ndata: ${JSON.stringify(metrics)}\n\n`,
    }),
  )

  await page.goto('/')
  const svg = page.getByTestId('web')
  const shopWeb = svg.locator('[data-id="app/shop/Deployment/web"]')
  await expect(shopWeb).toHaveCount(1)
  await expect(shopWeb).toHaveAttribute('data-top', '1')
  await expect(shopWeb).toHaveAttribute('data-size', '80')
  await expect(svg.locator('[data-id="app/visual-kubernetes/Deployment/visual-kubernetes"]')).toHaveCount(0)
  await expect(svg.locator('[data-id="app/visual-kubernetes/Pod/visual-kubernetes-probe"]')).toHaveCount(0)
  await expect(svg.locator('[data-id="networking.k8s.io/NetworkPolicy/visual-kubernetes/visual-kubernetes-deny-ingress"]')).toHaveCount(0)
  await expect(svg.locator('[data-id="app/kube-system/Deployment/visual-kubernetes"]')).toHaveCount(0)

  await page.locator('.f-pill').click()
  const nsGroup = page.getByRole('group', { name: 'Namespaces' })
  await expect(nsGroup.getByRole('button', { name: 'shop' })).toHaveCount(1)
  await expect(nsGroup.getByRole('button', { name: 'visual-kubernetes' })).toHaveCount(0)

  const selfSwitch = page.getByRole('switch', { name: 'Show visual-kubernetes' })
  await selfSwitch.click()
  await expect(svg.locator('[data-id="app/visual-kubernetes/Deployment/visual-kubernetes"]')).toHaveAttribute('data-top', '1')
  await expect(svg.locator('[data-id="app/kube-system/Deployment/visual-kubernetes"]')).toHaveCount(1)
  await expect(nsGroup.getByRole('button', { name: 'visual-kubernetes' })).toHaveCount(1)

  await selfSwitch.click()
  await expect(svg.locator('[data-id="app/visual-kubernetes/Deployment/visual-kubernetes"]')).toHaveCount(0)
  await expect(shopWeb).toHaveAttribute('data-top', '1')
})

test('workload kind and namespace filters highlight in place or isolate matching items', async ({ page }) => {
  await openPill(page)
  const svg = page.getByTestId('web')
  const webApp = svg.locator(`[data-id="${WEB_APP}"]`)
  const proxyApp = svg.locator(`[data-id="${PROXY_APP}"]`)
  await expect(webApp).toHaveAttribute('data-pods', '4', { timeout: 15_000 })
  await expect(proxyApp).toHaveCount(1)

  const modeGroup = page.getByRole('group', { name: 'Filter mode' })
  const kindsGroup = page.getByRole('group', { name: 'Workload kinds' })
  const nsGroup = page.getByRole('group', { name: 'Namespaces' })
  await expect(modeGroup.getByRole('button', { name: 'Highlight' })).toHaveAttribute('aria-pressed', 'true')

  // Highlight DaemonSet: kube-proxy stays bright while Deployment/web, nodes and hub dim in place.
  await kindsGroup.getByRole('button', { name: 'DaemonSet' }).click()
  await expect(proxyApp).not.toHaveAttribute('data-dimmed', 'true')
  await expect(webApp).toHaveAttribute('data-dimmed', 'true')
  await expect(svg.locator('[data-role="app"]')).toHaveCount(2)

  // Adding Deployment highlights both DaemonSets and Deployments (OR within kinds).
  await kindsGroup.getByRole('button', { name: 'Deployment' }).click()
  await expect(proxyApp).not.toHaveAttribute('data-dimmed', 'true')
  await expect(webApp).not.toHaveAttribute('data-dimmed', 'true')

  // Deployment + kube-system (AND across kind and namespace): neither web (shop) nor kube-proxy (DaemonSet) matches.
  await kindsGroup.getByRole('button', { name: 'DaemonSet' }).click()
  await nsGroup.getByRole('button', { name: 'kube-system' }).click()
  await expect(webApp).toHaveAttribute('data-dimmed', 'true')
  await expect(proxyApp).toHaveAttribute('data-dimmed', 'true')

  // Switch to Only show on namespace shop: only shop's web app, its hosting nodes and the API hub stay.
  await kindsGroup.getByRole('button', { name: 'Deployment' }).click()
  await nsGroup.getByRole('button', { name: 'kube-system' }).click()
  await modeGroup.getByRole('button', { name: 'Only show' }).click()
  await nsGroup.getByRole('button', { name: 'shop' }).click()
  await expect(webApp).toHaveCount(1)
  await expect(proxyApp).toHaveCount(0)
  await expect(svg.locator('[data-role="cp"]')).toHaveCount(0)
  await expect(svg.locator('[data-role="hub"]')).toHaveCount(1)
  await expect(svg).toHaveAttribute('data-auto', 'on')

  // Isolating StatefulSet when none exist draws 0 apps and 0 hosting nodes.
  await nsGroup.getByRole('button', { name: 'shop' }).click()
  await kindsGroup.getByRole('button', { name: 'StatefulSet' }).click()
  await expect(svg.locator('[data-role="app"]')).toHaveCount(0)
  await expect(svg.locator('[data-role="node"]')).toHaveCount(0)
  await expect(svg.locator('[data-role="hub"]')).toHaveCount(1)

  // All on resets both group switches and focus filters.
  await page.getByRole('button', { name: 'All on' }).click()
  await expect(page.locator('.f-pill')).toHaveText('Showing all')
  await expect(webApp).toHaveCount(1)
  await expect(proxyApp).toHaveCount(1)
  await expect(svg.locator('[data-dimmed="true"]')).toHaveCount(0)
  expect(await smallText(page)).toEqual([])
})

test('workload kind focus keeps linked standalone satellites and hosting nodes across multiple satellites', async ({
  page,
}) => {
  const ent = (id: string, kind: string, group: string, name: string, namespace: string, filterGroup: string) => ({
    id,
    kind,
    group,
    name,
    namespace,
    labels: {},
    status: '1/1 ready',
    filterGroup,
  })
  const snapshot = {
    entities: [
      ent('cp/apiserver', 'APIServer', 'cp', 'apiserver', '', 'controlPlane'),
      ent('core/Node/_/node-1', 'Node', 'core', 'node-1', '', 'nodes'),
      ent('core/Node/_/node-2', 'Node', 'core', 'node-2', '', 'nodes'),
      ent('app/shop/Deployment/web', 'Deployment', 'apps', 'web', 'shop', 'workloads'),
      ent('app/shop/StatefulSet/db', 'StatefulSet', 'apps', 'db', 'shop', 'workloads'),
      // First loose satellite is unmatched so any single-pass edge iterator is exhausted before web-ing and hostedNodes.
      ent('networking.k8s.io/Ingress/shop/db-ing', 'Ingress', 'networking.k8s.io', 'db-ing', 'shop', 'ingress'),
      ent('networking.k8s.io/Ingress/shop/web-ing', 'Ingress', 'networking.k8s.io', 'web-ing', 'shop', 'ingress'),
    ],
    edges: [
      { id: 'member:cp/apiserver->core/Node/_/node-1', from: 'cp/apiserver', to: 'core/Node/_/node-1', kind: 'member' },
      { id: 'member:cp/apiserver->core/Node/_/node-2', from: 'cp/apiserver', to: 'core/Node/_/node-2', kind: 'member' },
      { id: 'hosts:core/Node/_/node-1->app/shop/Deployment/web', from: 'core/Node/_/node-1', to: 'app/shop/Deployment/web', kind: 'hosts' },
      { id: 'hosts:core/Node/_/node-2->app/shop/StatefulSet/db', from: 'core/Node/_/node-2', to: 'app/shop/StatefulSet/db', kind: 'hosts' },
      { id: 'routes:networking.k8s.io/Ingress/shop/db-ing->app/shop/StatefulSet/db', from: 'networking.k8s.io/Ingress/shop/db-ing', to: 'app/shop/StatefulSet/db', kind: 'routes' },
      { id: 'routes:networking.k8s.io/Ingress/shop/web-ing->app/shop/Deployment/web', from: 'networking.k8s.io/Ingress/shop/web-ing', to: 'app/shop/Deployment/web', kind: 'routes' },
    ],
    capabilities: {},
  }
  await page.route('**/api/stream', (route) =>
    route.fulfill({
      status: 200,
      headers: { 'content-type': 'text/event-stream', 'cache-control': 'no-cache' },
      body: `retry: 60000\nevent: snapshot\ndata: ${JSON.stringify(snapshot)}\n\n`,
    }),
  )

  await page.goto('/')
  const svg = page.getByTestId('web')
  const webIng = svg.locator('[data-id="networking.k8s.io/Ingress/shop/web-ing"]')
  const dbIng = svg.locator('[data-id="networking.k8s.io/Ingress/shop/db-ing"]')
  await expect(webIng).toHaveCount(1)
  await expect(dbIng).toHaveCount(1)

  await page.locator('.f-pill').click()
  await page.getByRole('group', { name: 'Workload kinds' }).getByRole('button', { name: 'Deployment' }).click()
  await expect(webIng).not.toHaveAttribute('data-dimmed', 'true')
  await expect(dbIng).toHaveAttribute('data-dimmed', 'true')

  await page.getByRole('group', { name: 'Filter mode' }).getByRole('button', { name: 'Only show' }).click()
  await expect(svg.locator('[data-id="app/shop/Deployment/web"]')).toHaveCount(1)
  await expect(webIng).toHaveCount(1)
  await expect(dbIng).toHaveCount(0)
  await expect(svg.locator('[data-id="core/Node/_/node-1"]')).toHaveCount(1)
  await expect(svg.locator('[data-id="core/Node/_/node-2"]')).toHaveCount(0)
})

test('V hides and shows the pill', async ({ page }) => {
  await openPill(page)
  await page.keyboard.press('v')
  await expect(page.getByTestId('filter')).toHaveCount(0)
  await expect(page.getByTestId('web').locator('[data-role="node"]')).toHaveCount(3)
  await page.keyboard.press('v')
  await expect(page.getByTestId('filter')).toHaveCount(1)
})

test('V still hides the pill right after a switch is flipped', async ({ page }) => {
  await openPill(page)
  const pods = page.getByRole('switch', { name: 'Pods' })
  await pods.click()
  await expect(pods).toBeFocused() // the precondition the bug needs: focus sits on the switch
  await page.keyboard.press('v')
  await expect(page.getByTestId('filter')).toHaveCount(0)
})

test('a reload starts with every group on', async ({ page }) => {
  await openPill(page)
  await page.getByRole('switch', { name: 'Pods' }).click()
  await expect(page.locator('.f-pill')).toHaveText('7 of 8 shown')
  await page.reload()
  await expect(page.locator('.f-pill')).toHaveText('Showing all')
})
