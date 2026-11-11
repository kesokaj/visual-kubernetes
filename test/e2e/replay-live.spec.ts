import { expect, test, type Route } from '@playwright/test'

// Replay against a hand-written stream served through page.route (D45), for what a recording
// cannot show: a stream with deltas but no chain, and a chain that grows after playback has
// caught up with it, which is the normal live case (review phase 5, findings 3 and 4).

const DEP = 'apps/Deployment/demo/api'
const node = { id: 'core/Node/_/node-1', kind: 'Node', group: 'core', name: 'node-1', namespace: '', labels: {}, status: 'Ready', filterGroup: 'nodes' }
const app = { id: DEP, kind: 'Deployment', group: 'apps', name: 'api', namespace: 'demo', labels: {}, status: 'Ready', filterGroup: 'workloads' }
const VERBS = ['authentication', 'authorization', 'admission', 'persisted', 'created', 'updated']

const chainOf = (n: number) => ({
  id: `${DEP}@2026-11-10T10:00:00.000Z`,
  steps: VERBS.slice(0, n).map((verb, i) => ({ at: '2026-11-10T10:00:00.000Z', actor: 'kubectl', verb, object: DEP, derived: i < 4 })),
})

const frame = (event: string, data: unknown) => `event: ${event}\ndata: ${JSON.stringify(data)}\n\n`

// serve answers each (re)connect with the next body; EventSource reconnects after `retry` ms
// once a body ends. A body may wait on a promise, so a test decides when the next batch lands.
function serve(bodies: Array<() => Promise<string>>) {
  let n = 0
  return async (route: Route) => {
    const body = bodies[Math.min(n++, bodies.length - 1)]
    await route.fulfill({
      status: 200,
      headers: { 'content-type': 'text/event-stream', 'cache-control': 'no-cache' },
      body: await (body ?? (() => Promise.resolve('')))(),
    })
  }
}

test('the replay keys do nothing on a stream without a chain', async ({ page }) => {
  const snapshot = { entities: [node], edges: [] }
  await page.route('**/api/stream', serve([async () => `retry: 60000\n${frame('snapshot', snapshot)}${frame('upsert', { entity: app })}`]))
  await page.goto('/')
  await expect(page.getByTestId('web').locator(`[data-id="${DEP}"]`)).not.toHaveCount(0)
  for (const key of ['r', ' ', 'ArrowRight']) await page.keyboard.press(key)
  await page.waitForTimeout(500)
  await expect(page.getByTestId('chain-caption')).toHaveCount(0)
})

test('playback that caught up with a growing chain resumes when the chain grows', async ({ page }) => {
  test.setTimeout(30_000)
  const snapshot = { entities: [node, app], edges: [] }
  let release = () => {}
  const later = new Promise<void>((done) => (release = done))
  await page.route(
    '**/api/stream',
    serve([
      async () => `retry: 200\n${frame('snapshot', snapshot)}${frame('chain', chainOf(2))}`,
      async () => {
        await later
        return `retry: 60000\n${frame('snapshot', snapshot)}${frame('chain', chainOf(5))}`
      },
    ]),
  )
  await page.goto('/')
  await expect(page.getByTestId('web').locator(`[data-id="${DEP}"]`)).not.toHaveCount(0)
  await page.keyboard.press('r')
  const cap = page.getByTestId('chain-caption')
  await expect(cap).toHaveAttribute('data-step', '2', { timeout: 2_500 })
  await expect(cap).toHaveAttribute('data-steps', '2')
  await page.waitForTimeout(1_500) // caught up: no tick is pending any more
  await expect(cap).toHaveAttribute('data-step', '2')
  release()
  await expect(cap).toHaveAttribute('data-steps', '5', { timeout: 3_000 })
  await expect(cap).toHaveAttribute('data-step', '5', { timeout: 5_000 }) // no key pressed
  await expect(cap).toHaveAttribute('data-paused', 'false')
})
