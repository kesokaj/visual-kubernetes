import { expect, test, type Page } from '@playwright/test'
import { smallText } from './checks'
import { urlOf } from './servers'

// The size-metric switch in the filter pill (PRD C3): Auto keeps the server's sizeBy fallback
// (PRD S2), Network, CPU, Memory and Pods pin one metric. The telemetry stream gives each metric
// a different leader (network: web, CPU: cart, memory: batch, pods: web with 3), so the top-1
// halo proves which metric sized the web. Assertions read data-* attributes (ADR-004).

const web = (page: Page) => page.getByTestId('web')
const body = (page: Page, id: string) => web(page).locator(`[data-layer="bodies"] > g[data-id="${id}"]`)
const app = (name: string) => `app/shop/Deployment/${name}`
const sizeBtn = (page: Page, size: string) => page.locator(`[data-testid="size-switch"] button[data-size="${size}"]`)
const iconOf = async (page: Page, name: string) => Number(await body(page, app(name)).getAttribute('data-size'))

async function openPill(page: Page) {
  await page.goto('/')
  await page.locator('.f-pill').click()
  await expect(page.getByTestId('size-switch')).toBeVisible()
}

async function pick(page: Page, size: string, metric: string, leader: string) {
  await sizeBtn(page, size).click()
  await expect(sizeBtn(page, size)).toHaveAttribute('aria-pressed', 'true')
  await expect(web(page)).toHaveAttribute('data-size-by', metric)
  await expect(body(page, app(leader))).toHaveAttribute('data-top', '1')
}

test.describe('telemetry: the switch resizes and re-ranks apps', () => {
  test.use({ baseURL: urlOf('telemetry') })

  test('Auto follows the server; Network, CPU, Memory and Pods each pin their metric', async ({ page }) => {
    await openPill(page)
    // Auto is the default and names the server's pick
    await expect(sizeBtn(page, 'auto')).toHaveAttribute('aria-pressed', 'true')
    await expect(web(page)).toHaveAttribute('data-size-by', 'network', { timeout: 10_000 })
    await expect(page.locator('[data-testid="size-switch"] .f-hint')).toHaveText('auto: Network')
    await expect(page.locator('.f-pill')).toHaveText('Showing all')
    for (const s of ['network', 'cpu', 'memory', 'pods']) await expect(sizeBtn(page, s)).toBeEnabled()

    await pick(page, 'cpu', 'cpu', 'cart')
    await expect(page.locator('.f-pill')).toHaveText('Showing all · size: CPU')
    await expect(page.locator('[data-testid="size-switch"] .f-hint')).toHaveCount(0)
    await expect.poll(async () => (await iconOf(page, 'cart')) > (await iconOf(page, 'web'))).toBe(true)

    await pick(page, 'memory', 'memory', 'batch')
    await expect(page.locator('.f-pill')).toHaveText('Showing all · size: Memory')
    await expect.poll(async () => (await iconOf(page, 'batch')) > (await iconOf(page, 'web'))).toBe(true)
    await expect(web(page).locator('.halo')).toHaveCount(3)

    await pick(page, 'pods', 'pods', 'web')
    await expect(body(page, app('api'))).toHaveAttribute('data-top', '2')
    // cart and batch run one pod each: pinned to pods, they size alike whatever their samples say
    await expect.poll(async () => (await iconOf(page, 'cart')) === (await iconOf(page, 'batch'))).toBe(true)

    await pick(page, 'network', 'network', 'web')
    await expect(body(page, app('api'))).toHaveAttribute('data-top', '2')

    await pick(page, 'auto', 'network', 'web')
    await expect(page.locator('.f-pill')).toHaveText('Showing all')
    expect(await smallText(page)).toEqual([])
  })

  test('the switch fits the pill, and All on resets it to Auto', async ({ page }) => {
    await openPill(page)
    const group = page.getByRole('group', { name: 'Size apps by' })
    await expect(group.getByRole('button')).toHaveText(['Auto', 'Network', 'CPU', 'Memory', 'Pods'])
    const fits = await group.evaluate((el) => el.scrollWidth <= el.clientWidth && el.getBoundingClientRect().right <= (el.closest('.f-list')?.getBoundingClientRect().right ?? 0))
    expect(fits).toBe(true)
    await pick(page, 'memory', 'memory', 'batch')
    await page.getByRole('button', { name: 'All on' }).click()
    await expect(sizeBtn(page, 'auto')).toHaveAttribute('aria-pressed', 'true')
    await expect(web(page)).toHaveAttribute('data-size-by', 'network')
  })
})

test.describe('missing sources: metrics the cluster does not report are disabled', () => {
  test.use({ baseURL: urlOf('missingSources') })

  test('only Auto and Pods are offered when no metrics arrive', async ({ page }) => {
    await openPill(page)
    await expect(web(page)).toHaveAttribute('data-size-by', 'pods')
    await expect(page.locator('[data-testid="size-switch"] .f-hint')).toHaveText('auto: Pods')
    for (const s of ['network', 'cpu', 'memory']) {
      await expect(sizeBtn(page, s)).toBeDisabled()
      await expect(sizeBtn(page, s)).toHaveAttribute('title', /reports no/)
    }
    await expect(sizeBtn(page, 'auto')).toBeEnabled()
    await expect(sizeBtn(page, 'pods')).toBeEnabled()
    await sizeBtn(page, 'pods').click()
    await expect(page.locator('.f-pill')).toHaveText('Showing all · size: Pods')
    await expect(web(page)).toHaveAttribute('data-size-by', 'pods')
  })
})
