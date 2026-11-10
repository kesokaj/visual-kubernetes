import { expect, test, type Page } from '@playwright/test'
import { WEB_APP } from './checks'

// The filter pill (plan D3): eight groups, all on by default; a hidden group leaves the
// drawing and the layout, and the camera re-fits; V hides the pill.

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

async function openPill(page: Page) {
  await page.goto('/')
  await expect(page.getByTestId('web').locator('[data-role="node"]')).toHaveCount(3)
  await page.locator('.f-pill').click()
  await expect(page.getByRole('switch')).toHaveCount(8)
}

test('eight groups, all on by default', async ({ page }) => {
  await openPill(page)
  await expect(page.locator('.f-pill')).toHaveText('Showing all')
  await expect(page.locator('.f-row span')).toHaveText(GROUP_LABELS)
  for (const label of GROUP_LABELS) await expect(page.getByRole('switch', { name: label })).toBeChecked()
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
