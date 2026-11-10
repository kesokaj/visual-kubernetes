import { expect, test } from '@playwright/test'
import { smallText } from './checks'

// The 150-bubble budget (PRD S5, folding.ts MAX_BUBBLES). No recorded stream exceeds it (the
// model folds large-cluster to about 107 bodies), so this spec serves a hand-written snapshot
// of 220 apps through page.route, as wire.spec.ts does. The input alone is over the budget,
// so the assertions fail if keepBudget stops trimming.

const ent = (id: string, kind: string, group: string, name: string, filterGroup: string, extra = {}) => ({
  id, kind, group, name, namespace: id.startsWith('app/') ? 'load' : '', labels: {}, status: 'Running', filterGroup, ...extra,
})

test('more than 150 bodies fold the smallest into an over-budget corner counter', async ({ page }) => {
  const apps = Array.from({ length: 220 }, (_, i) => {
    const n = `svc-${String(i).padStart(3, '0')}`
    return ent(`app/load/Deployment/${n}`, 'Deployment', 'apps', n, 'workloads', { ownerId: `apps/Deployment/load/${n}` })
  })
  const snapshot = {
    entities: [ent('cp/apiserver', 'APIServer', 'cp', 'apiserver', 'controlPlane'), ent('core/Node/_/n1', 'Node', 'core', 'n1', 'nodes'), ...apps],
    edges: [],
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
  await expect(svg.locator('[data-role="hub"]')).toHaveCount(1)
  await expect(svg).toHaveAttribute('data-bubbles', '150')
  await expect(svg.locator('[data-layer="bodies"] > g[data-id]')).toHaveCount(150)
  // the hub and the node are always kept; 72 apps fold into the page's counter
  await expect(svg.locator('[data-role="node"]')).toHaveCount(1)
  const over = page.getByTestId('counters').locator('[data-counter="counter/page.overBudget"]')
  await expect(over).toHaveAttribute('data-count', '72')
  await expect(over).toBeVisible()
  expect(await smallText(page)).toEqual([])
})
