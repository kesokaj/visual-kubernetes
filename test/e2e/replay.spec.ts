import { expect, test, type Page } from '@playwright/test'
import { WEB_APP, smallText } from './checks'
import { urlOf } from './servers'

// Causal-chain replay (plan Phase 5, D17). deploy-scale carries one chain, resent as it grows:
// the Deployment scaled from 2 to 4 replicas, through the API server, the ReplicaSet, two new
// pods, their scheduling, start and readiness, and the EndpointSlice that lists them. R replays
// the last chain at about one step a second, Space pauses and resumes, → shows the next step,
// Esc hides the caption. deployScaleReplay is opened by this spec only: a later page would get
// the snapshot without the chain.

const CHAIN = 'apps/Deployment/shop/web@2026-11-10T10:00:01.000Z'
const DERIVED = ['authentication', 'authorization', 'admission', 'persisted']
const OBSERVED = ['updated', 'updated', 'created', 'created', 'scheduled', 'scheduled', 'started', 'started', 'ready', 'ready', 'updated']
const D17 = [...DERIVED, ...OBSERVED]
const HUB = 'cp/apiserver'

type Seen = { step: number; verb: string; target: string; t: number }

// watchPlayback samples the caption every frame until it reaches its last step (or 30 s pass)
// and returns each step as it first appeared, with the time it did.
function watchPlayback(page: Page): Promise<Seen[]> {
  return page.evaluate(
    () =>
      new Promise<Seen[]>((done) => {
        const out: Seen[] = []
        const t0 = performance.now()
        const frame = () => {
          const el = document.querySelector('[data-testid="chain-caption"]')
          const step = Number(el?.getAttribute('data-step') ?? 0)
          if (el && step !== out.at(-1)?.step) {
            out.push({ step, verb: el.getAttribute('data-verb') ?? '', target: el.getAttribute('data-target') ?? '', t: performance.now() })
          }
          if ((el && step === Number(el.getAttribute('data-steps'))) || performance.now() - t0 > 30_000) done(out)
          else requestAnimationFrame(frame)
        }
        frame()
      }),
  )
}

const median = (xs: number[]) => [...xs].sort((a, b) => a - b)[Math.floor(xs.length / 2)] ?? NaN

test.describe('replay of a recorded chain', () => {
  test.use({ baseURL: urlOf('deployScaleReplay') })

  test('R replays the scale chain in D17 order at one step a second; Space pauses, → steps', async ({ page }) => {
    test.setTimeout(90_000)
    await page.goto('/')
    const cap = page.getByTestId('chain-caption')
    await expect(page.getByTestId('web').locator(`[data-id="${WEB_APP}"]`)).toHaveCount(1)
    await expect(cap).toHaveCount(0)

    await test.step('wait for the whole chain (its last copy has 15 steps)', async () => {
      await expect
        .poll(async () => {
          await page.keyboard.press('r')
          return (await cap.count()) === 0 ? null : cap.getAttribute('data-steps')
        }, { timeout: 20_000 })
        .toBe(String(D17.length))
      await expect(cap).toHaveAttribute('data-chain-id', CHAIN)
    })

    await test.step('R plays every step in order, derived steps at the API server', async () => {
      await page.keyboard.press('Escape')
      await expect(cap).toHaveCount(0)
      await page.keyboard.press('r')
      const seen = await watchPlayback(page)
      expect(seen.map((s) => s.step)).toEqual(D17.map((_, i) => i + 1))
      expect(seen.map((s) => s.verb)).toEqual(D17)
      expect(seen.slice(0, DERIVED.length).map((s) => s.target)).toEqual(DERIVED.map(() => HUB))
      // the Deployment, its ReplicaSet and the new pods all point at the web app's bubble
      expect(seen.slice(DERIVED.length, D17.length - 1).every((s) => s.target === WEB_APP)).toBe(true)
      const gaps = seen.slice(1).map((s, i) => s.t - (seen[i]?.t ?? Number.NaN))
      expect(median(gaps)).toBeGreaterThanOrEqual(900)
      expect(median(gaps)).toBeLessThanOrEqual(1200)
      expect(Math.min(...gaps)).toBeGreaterThanOrEqual(800)
    })

    await test.step('Space pauses, → shows exactly one more step, Space resumes', async () => {
      await page.keyboard.press('r')
      await expect(cap).toHaveAttribute('data-step', '2', { timeout: 3_000 })
      await page.keyboard.press(' ')
      await expect(cap).toHaveAttribute('data-paused', 'true')
      await expect(cap).toContainText('paused')
      const held = Number(await cap.getAttribute('data-step'))
      await page.waitForTimeout(1_500)
      await expect(cap).toHaveAttribute('data-step', String(held))
      await page.keyboard.press('ArrowRight')
      await expect(cap).toHaveAttribute('data-step', String(held + 1))
      await page.waitForTimeout(1_500)
      await expect(cap).toHaveAttribute('data-step', String(held + 1))
      await expect(cap).toHaveAttribute('data-paused', 'true')
      expect(await smallText(page)).toEqual([])
      await page.keyboard.press(' ')
      await expect(cap).toHaveAttribute('data-paused', 'false')
      await expect(cap).toHaveAttribute('data-step', String(held + 2), { timeout: 2_500 })
    })

    await test.step('R starts over from step 1, Esc hides the caption', async () => {
      await page.keyboard.press('r')
      await expect(cap).toHaveAttribute('data-step', '1')
      await expect(cap).toHaveAttribute('data-verb', 'authentication')
      await expect(cap).toContainText('inferred')
      await page.keyboard.press('Escape')
      await expect(cap).toHaveCount(0)
    })
  })
})
