import { expect, test } from '@playwright/test'

// The stream is a trust boundary: payloads failing their guard are dropped item by item.
// These tests serve a hand-written stream through page.route instead of the replay.

const node = (name: string) => ({
  id: `core/Node/_/${name}`,
  kind: 'Node',
  group: 'core',
  name,
  namespace: '',
  labels: {},
  status: 'Ready',
  filterGroup: 'nodes',
})

test('one unknown item does not blank the snapshot (review phase 3, finding 2)', async ({ page }) => {
  const snapshot = {
    entities: [node('node-a'), node('node-b'), { ...node('node-x'), filterGroup: 'mesh' }],
    edges: [{ id: 'e1', from: 'core/Node/_/node-a', to: 'core/Node/_/node-b', kind: 'peers' }],
  }
  await page.route('**/api/stream', (route) =>
    route.fulfill({
      status: 200,
      headers: { 'content-type': 'text/event-stream', 'cache-control': 'no-cache' },
      body: `retry: 60000\nevent: snapshot\ndata: ${JSON.stringify(snapshot)}\n\n`,
    }),
  )
  const warnings: string[] = []
  page.on('console', (m) => m.type() === 'warning' && warnings.push(m.text()))
  await page.goto('/')
  const svg = page.getByTestId('web')
  await expect(svg.locator('[data-role="node"]')).toHaveCount(2)
  await expect(svg.locator('[data-id="core/Node/_/node-x"]')).toHaveCount(0)
  expect(warnings).toContain('snapshot: dropped 1 invalid entities')
  expect(warnings).toContain('snapshot: dropped 1 invalid edges')
})
