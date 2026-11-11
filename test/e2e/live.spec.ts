import { execFile } from 'node:child_process'
import { readFileSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { promisify } from 'node:util'
import { expect, test, type Page } from '@playwright/test'
import { foreignRequests, recordCspViolations, recordRequests, smallText } from './checks'

// Live end-to-end suite against an installed cluster (plan Phase 7, Box 7.5; invoked by
// test/e2e/live.sh with VK_LIVE_URL and KUBECONFIG). Covers:
//   1. ServiceAccount `kubectl auth can-i --list` read-only invariant (R1, R2) + must-fail checks
//   2. Golden path: apply examples/01-deployment.yaml, scale to 5 pods, measure G2 pulse latency
//      p95 <= 2 s over 10 settled scales, and replay the live causal chain with R / Space / -> / Esc
//   3. Filter pill: switching off Pods hides every pod and re-fits; reload restores all 8 groups
//   4. Missing optional source: without metrics.k8s.io APIService, workloads still render and size
//      by fallback while cp/apiserver lists metrics.k8s.io as `missing`

const execFileAsync = promisify(execFile)
const repoRoot = process.env.VK_REPO_ROOT ?? path.resolve(process.cwd(), '..')
const scratchDir = process.env.VK_LIVE_SCRATCH ?? path.join(repoRoot, '.agents/scratch/e2e/live')
const NS = 'vk-live'
const HELLO_APP = `app/${NS}/Deployment/hello`
const SA = 'system:serviceaccount:visual-kubernetes:visual-kubernetes'

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

const SELF_SUBJECT_REVIEWS = new Set([
  'selfsubjectreviews.authentication.k8s.io',
  'selfsubjectaccessreviews.authorization.k8s.io',
  'selfsubjectrulesreviews.authorization.k8s.io',
])

const FORBIDDEN_SUBRESOURCES = new Set([
  'nodes/proxy',
  'pods/exec',
  'pods/attach',
  'pods/portforward',
  'serviceaccounts/token',
])

async function kubectl(...args: string[]): Promise<string> {
  const { stdout } = await execFileAsync('kubectl', args, {
    env: process.env,
    maxBuffer: 8 * 1024 * 1024,
  })
  return stdout
}

// validateCanIList parses `kubectl auth can-i --list` output fail-closed and returns every
// rule violation against PRD R1 (read-only verbs) and PRD R2 (no Secrets except named Hubble cert).
export function validateCanIList(raw: string): string[] {
  const lines = raw
    .split(/\r?\n/)
    .map((l) => l.trimEnd())
    .filter((l) => l.trim().length > 0)
  if (lines.length < 2 || !lines[0]?.startsWith('Resources')) {
    return ['missing kubectl auth can-i --list header or rules']
  }
  const issues: string[] = []
  let sawPodsRead = false
  const rowRe = /^\s*(\S*)\s+\[([^\]]*)\]\s+\[([^\]]*)\]\s+\[([^\]]*)\]\s*$/

  for (const line of lines.slice(1)) {
    const m = rowRe.exec(line)
    if (!m) {
      issues.push(`unparseable rule row: ${line}`)
      continue
    }
    const resource = m[1] ?? ''
    const nonResource = (m[2] ?? '').trim()
    const names = (m[3] ?? '').trim().split(/\s+/).filter(Boolean)
    const verbs = (m[4] ?? '').trim().split(/\s+/).filter(Boolean)

    if (verbs.length === 0) {
      issues.push(`empty verbs in row: ${line}`)
      continue
    }
    if (SELF_SUBJECT_REVIEWS.has(resource) && nonResource === '' && names.length === 0) {
      if (verbs.some((v) => v !== 'create')) {
        issues.push(`unexpected verb on ${resource}: ${verbs.join(',')}`)
      }
      continue
    }
    if (resource.includes('*') || FORBIDDEN_SUBRESOURCES.has(resource)) {
      issues.push(`forbidden resource ${resource}: ${line}`)
    }
    for (const v of verbs) {
      if (v !== 'get' && v !== 'list' && v !== 'watch') {
        issues.push(`write or wildcard verb ${v} on ${resource || nonResource}`)
      }
    }
    if (resource === 'secrets' || resource.startsWith('secrets/') || resource.endsWith('.secrets')) {
      if (verbs.length !== 1 || verbs[0] !== 'get' || names.length !== 1 || names[0] !== 'hubble-relay-client-certs') {
        issues.push(`unscoped or over-privileged secrets rule: ${line}`)
      }
    }
    if (resource === 'pods' && verbs.includes('get') && verbs.includes('list') && verbs.includes('watch')) {
      sawPodsRead = true
    }
  }
  if (!sawPodsRead) {
    issues.push('positive control failed: pods [get list watch] not present')
  }
  return issues
}

function p95(values: readonly number[]): number {
  const sorted = [...values].sort((a, b) => a - b)
  const idx = Math.max(0, Math.ceil(0.95 * sorted.length) - 1)
  return sorted[idx] ?? Number.POSITIVE_INFINITY
}

async function waitHelloSettled(page: Page, replicas: number) {
  await kubectl('-n', NS, 'rollout', 'status', 'deployment/hello', '--timeout=90s')
  await expect
    .poll(
      async () => {
        const out = await kubectl('-n', NS, 'get', 'pods', '-l', 'app.kubernetes.io/name=hello', '--no-headers')
        return out
          .split('\n')
          .map((l) => l.trim())
          .filter(Boolean).length
      },
      { timeout: 60_000 },
    )
    .toBe(replicas)
  const app = page.getByTestId('web').locator(`[data-layer="bodies"] > g[data-id="${HELLO_APP}"]`)
  await expect(app).toHaveAttribute('data-pods', String(replicas), { timeout: 15_000 })
  await page.waitForTimeout(250)
}

function waitNextPulse(page: Page, appId: string): Promise<number> {
  return page.evaluate((id) => {
    const el = document.querySelector(`[data-layer="bodies"] > g[data-id="${id}"]`)
    if (!el) throw new Error(`missing app group ${id}`)
    const baseline = Number(el.getAttribute('data-pulses') ?? '0')
    return new Promise<number>((resolve, reject) => {
      const timer = setTimeout(() => {
        obs.disconnect()
        reject(new Error(`timed out waiting for data-pulses > ${baseline} on ${id}`))
      }, 10_000)
      const check = () => {
        const cur = Number(el.getAttribute('data-pulses') ?? '0')
        if (cur > baseline) {
          clearTimeout(timer)
          obs.disconnect()
          resolve(Date.now())
        }
      }
      const obs = new MutationObserver(check)
      obs.observe(el, { attributes: true, attributeFilter: ['data-pulses'] })
      check()
    })
  }, appId)
}

async function clickBody(page: Page, id: string) {
  await page.getByTestId('web').locator(`[data-layer="bodies"] > g[data-id="${id}"] > use`).first().click()
}

test.describe.serial('live cluster e2e', () => {
  test.setTimeout(240_000)

  test('ServiceAccount auth can-i --list grants only get/list/watch and no unscoped Secrets (R1, R2)', async () => {
    const clusterWide = await kubectl('auth', 'can-i', '--list', `--as=${SA}`)
    // Validate the guard in its must-fail states before trusting a clean pass.
    expect(validateCanIList('')).not.toEqual([])
    expect(validateCanIList('Resources Non-Resource URLs Resource Names Verbs\nconfigmaps [] [] [get]\n')).not.toEqual([])
    expect(validateCanIList(`${clusterWide}\npods [] [] [get list watch patch]\n`)).not.toEqual([])
    expect(validateCanIList(`${clusterWide}\nsecrets [] [] [get]\n`)).not.toEqual([])
    expect(validateCanIList(`${clusterWide}\nsecrets [] [hubble-relay-client-certs] [get list]\n`)).not.toEqual([])
    expect(validateCanIList(`${clusterWide}\nnodes/proxy [] [] [get]\n`)).not.toEqual([])
    expect(validateCanIList(`${clusterWide}\n* [] [] [get]\n`)).not.toEqual([])

    expect(validateCanIList(clusterWide)).toEqual([])
    for (const ns of ['default', 'kube-system', 'visual-kubernetes']) {
      const scoped = await kubectl('auth', 'can-i', '--list', '-n', ns, `--as=${SA}`)
      expect(validateCanIList(scoped)).toEqual([])
    }
  })

  test('golden path: apply 01-deployment, scale to 5, G2 p95 <= 2 s over 10 scales, and R/Space/-> replay', async ({
    page,
  }) => {
    await kubectl('delete', 'namespace', NS, '--ignore-not-found', '--wait=true', '--timeout=120s')
    await kubectl('create', 'namespace', NS)

    const urls = recordRequests(page)
    const cspBlocked = await recordCspViolations(page)
    await page.goto('/')

    const svg = page.getByTestId('web')
    await expect(svg.locator('[data-role="hub"]')).toHaveCount(1, { timeout: 15_000 })
    expect(await svg.locator('[data-role="node"]').count()).toBeGreaterThanOrEqual(1)
    await expect(svg.locator('[data-id^="app/visual-kubernetes/"]')).toHaveCount(0)

    const hello = svg.locator(`[data-layer="bodies"] > g[data-id="${HELLO_APP}"]`)
    await expect(hello).toHaveCount(0)

    await kubectl('-n', NS, 'apply', '-f', path.join(repoRoot, 'examples/01-deployment.yaml'))
    await expect(hello).toHaveCount(1, { timeout: 15_000 })
    await expect.poll(async () => Number(await hello.getAttribute('data-pulses'))).toBeGreaterThan(0)
    await waitHelloSettled(page, 2)

    await kubectl('-n', NS, 'scale', 'deployment/hello', '--replicas=5')
    await waitHelloSettled(page, 5)

    // Measure G2 over 10 settled scale operations (5 -> 4 -> 5 ... -> 5).
    const endToEndSamples: number[] = []
    const postApiSamples: number[] = []
    for (let i = 0; i < 10; i++) {
      const target = i % 2 === 0 ? 4 : 5
      const pulseAtP = waitNextPulse(page, HELLO_APP)
      const tSend = Date.now()
      await kubectl('-n', NS, 'scale', 'deployment/hello', `--replicas=${target}`)
      const tApi = Date.now()
      const tPulse = await pulseAtP
      endToEndSamples.push(tPulse - tSend)
      postApiSamples.push(Math.max(0, tPulse - tApi))
      await waitHelloSettled(page, target)
    }

    const g2EndToEndP95 = p95(endToEndSamples)
    const g2PostApiP95 = p95(postApiSamples)
    console.log(
      `G2 p95 = ${g2EndToEndP95} ms end-to-end (${g2PostApiP95} ms post-API); samples ms = [${endToEndSamples.join(', ')}]`,
    )
    expect(g2EndToEndP95).toBeLessThanOrEqual(2_000)
    expect(g2PostApiP95).toBeLessThanOrEqual(2_000)

    // Replay the live causal chain with R, pause with Space, step with ->, resume with Space, close with Esc.
    const cap = page.getByTestId('chain-caption')
    await page.keyboard.press('r')
    await expect(cap).toHaveCount(1)
    await expect(cap).toHaveAttribute('data-step', '1')
    await expect(cap).toHaveAttribute('data-verb', 'authentication')
    await expect(cap).toHaveAttribute('data-target', 'cp/apiserver')
    expect(await cap.getAttribute('data-chain-id')).toMatch(/^apps\/Deployment\/vk-live\/hello@/)

    await expect(cap).toHaveAttribute('data-step', '2', { timeout: 3_000 })
    await page.keyboard.press(' ')
    await expect(cap).toHaveAttribute('data-paused', 'true')
    const heldStep = Number(await cap.getAttribute('data-step'))
    await page.waitForTimeout(1_200)
    await expect(cap).toHaveAttribute('data-step', String(heldStep))

    await page.keyboard.press('ArrowRight')
    await expect(cap).toHaveAttribute('data-step', String(heldStep + 1))
    await expect(cap).toHaveAttribute('data-paused', 'true')

    await page.keyboard.press(' ')
    await expect(cap).toHaveAttribute('data-paused', 'false')
    await expect
      .poll(async () => Number(await cap.getAttribute('data-step')), { timeout: 5_000 })
      .toBeGreaterThanOrEqual(5)
    await expect(cap).toHaveAttribute('data-target', HELLO_APP)

    expect(await smallText(page)).toEqual([])
    await page.keyboard.press('Escape')
    await expect(cap).toHaveCount(0)

    expect(cspBlocked).toEqual([])
    expect(foreignRequests(urls, new URL(page.url()).origin)).toEqual([])
  })

  test('filter: switching off Pods hides every pod and re-fits; reload restores all 8 groups', async ({ page }) => {
    await page.goto('/')
    const svg = page.getByTestId('web')
    await expect(svg.locator(`[data-layer="bodies"] > g[data-id="${HELLO_APP}"]`)).toHaveCount(1, { timeout: 15_000 })
    expect(await svg.locator('[data-role="pod"]').count()).toBeGreaterThanOrEqual(5)

    await page.mouse.move(900, 300)
    await page.mouse.wheel(0, -400)
    await expect(svg).toHaveAttribute('data-auto', 'off')

    await page.locator('.f-pill').click()
    await expect(page.locator('.f-pill')).toHaveText('Showing all')
    for (const label of GROUP_LABELS) {
      await expect(page.getByRole('switch', { name: label })).toBeChecked()
    }

    await page.getByRole('switch', { name: 'Pods' }).click()
    await expect(svg.locator('[data-role="pod"]')).toHaveCount(0)
    await expect(svg).toHaveAttribute('data-auto', 'on')
    await expect(page.locator('.f-pill')).toHaveText('7 of 8 shown')

    await page.reload()
    await expect(svg.locator(`[data-layer="bodies"] > g[data-id="${HELLO_APP}"]`)).toHaveCount(1, { timeout: 15_000 })
    expect(await svg.locator('[data-role="pod"]').count()).toBeGreaterThanOrEqual(5)
    await page.locator('.f-pill').click()
    await expect(page.locator('.f-pill')).toHaveText('Showing all')
    for (const label of GROUP_LABELS) {
      await expect(page.getByRole('switch', { name: label })).toBeChecked()
    }
    expect(await smallText(page)).toEqual([])
  })

  test('edge without metrics-server: workloads render with fallback sizing and cp/apiserver reports missing', async ({
    page,
  }) => {
    await page.goto('/')
    const svg = page.getByTestId('web')
    const hello = svg.locator(`[data-layer="bodies"] > g[data-id="${HELLO_APP}"]`)
    await expect(hello).toHaveCount(1, { timeout: 15_000 })

    await clickBody(page, 'cp/apiserver')
    const panel = page.getByTestId('panel')
    const metricsCap = panel.locator('li[data-cap="metrics.k8s.io"]')
    await expect(metricsCap).toHaveAttribute('data-state', 'ok', { timeout: 15_000 })

    const backupPath = path.join(scratchDir, 'metrics-apiservice.json')
    const rawJson = JSON.parse(await kubectl('get', 'apiservice', 'v1beta1.metrics.k8s.io', '-o', 'json'))
    delete rawJson.metadata.resourceVersion
    delete rawJson.metadata.uid
    delete rawJson.metadata.creationTimestamp
    delete rawJson.metadata.managedFields
    delete rawJson.status
    writeFileSync(backupPath, JSON.stringify(rawJson, null, 2))

    try {
      await kubectl('delete', 'apiservice', 'v1beta1.metrics.k8s.io')
      await expect(metricsCap).toHaveAttribute('data-state', 'missing', { timeout: 20_000 })
      await expect(hello).toHaveCount(1)
      await expect(hello).toHaveAttribute('data-pods', '5')
      expect(Number(await hello.getAttribute('data-size'))).toBeGreaterThan(0)
      expect(['network', 'pods']).toContain(await svg.getAttribute('data-size-by'))
      expect(await smallText(page)).toEqual([])
    } finally {
      const saved = readFileSync(backupPath, 'utf8')
      if (saved.trim().length > 0) {
        await kubectl('apply', '-f', backupPath)
      }
    }

    await expect(metricsCap).toHaveAttribute('data-state', 'ok', { timeout: 25_000 })
  })
})
