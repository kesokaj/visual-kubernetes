import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test, type Page } from '@playwright/test'
import { smallText } from './checks'
import { urlOf } from './servers'

// Folding (plan Phase 4, PRD S5, D30): corner counters for what is not drawn one by one, a
// 150-bubble budget, and static control-plane pods folded behind their cp/* part.

const web = (page: Page) => page.getByTestId('web')
const bodies = (page: Page) => web(page).locator('[data-layer="bodies"] > g[data-id]')
const body = (page: Page, id: string) => web(page).locator(`[data-layer="bodies"] > g[data-id="${id}"]`)

// foldedIds reads the entities the recorded snapshot marks folded (S5), so the spec does not
// hard-code which of the 600 objects the model chose.
function foldedIds(): string[] {
  const file = join(test.info().project.testDir, '..', 'fixtures', 'streams', 'large-cluster.ndjson')
  const first = readFileSync(file, 'utf8').split('\n', 1)[0] ?? '{}'
  const snap = JSON.parse(first) as { data?: { entities?: { id: string; folded?: boolean }[] } }
  return (snap.data?.entities ?? []).filter((e) => e.folded).map((e) => e.id)
}

test.describe('large cluster', () => {
  test.use({ baseURL: urlOf('largeCluster') })

  test('folded apps and pods become corner counters and the web stays under 150 bubbles', async ({ page }) => {
    await page.goto('/')
    await expect(web(page).locator('[data-role="node"]')).not.toHaveCount(0)
    const counters = page.getByTestId('counters')
    await expect(counters.locator('[data-counter="counter/folded.apps"]')).toBeVisible()
    await expect(counters.locator('[data-counter="counter/folded.pods"]')).toBeVisible()
    await expect(counters.locator('[data-counter="counter/folded.apps"]')).toContainText('apps folded')
    expect(Number(await web(page).getAttribute('data-bubbles'))).toBeLessThanOrEqual(150)
    expect(await bodies(page).count()).toBeLessThanOrEqual(150)
    // no folded entity is drawn, and no counter is drawn as a bubble
    const folded = foldedIds()
    expect(folded.length).toBeGreaterThan(30)
    const drawn = new Set(await bodies(page).evaluateAll((els) => els.map((e) => e.getAttribute('data-id'))))
    expect(folded.filter((id) => drawn.has(id))).toEqual([])
    expect([...drawn].filter((id) => id?.startsWith('counter/'))).toEqual([])
    // the counter follows the stream: 102 pods folded, then 104 and 106 as node-26 joins
    await expect(counters.locator('[data-counter="counter/folded.pods"]')).toHaveAttribute('data-count', '106', { timeout: 15_000 })
    expect(await smallText(page)).toEqual([])
  })
})

test.describe('static control-plane pods', () => {
  test.use({ baseURL: urlOf('telemetry') })

  test('fold behind their cp/* part, and only when that part exists', async ({ page }) => {
    await page.goto('/')
    await expect(body(page, 'cp/etcd')).toHaveCount(1)
    for (const comp of ['kube-apiserver', 'etcd']) {
      await expect(body(page, `app/kube-system/Pod/${comp}-node-1`)).toHaveCount(0)
      await expect(web(page).locator(`[data-role="pod"][data-id="core/Pod/kube-system/${comp}-node-1"]`)).toHaveCount(0)
    }
    await expect(body(page, 'cp/apiserver')).toHaveAttribute('data-statics', '1')
    await expect(body(page, 'cp/etcd')).toHaveAttribute('data-statics', '1')
    // no cp/scheduler in this stream: the scheduler's static pod stays drawn as its own app
    const sched = body(page, 'app/kube-system/Pod/kube-scheduler-node-1')
    await expect(sched).toHaveCount(1)
    await expect(sched.locator('[data-role="pod"]')).toHaveCount(1)
  })
})
