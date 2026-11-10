import playwright from '@playwright/test'

// web/package.json is "type": "module", so Node loads this file as native ESM, and
// @playwright/test is CommonJS without static named exports: take them off the default,
// which at runtime is module.exports (its typings declare it as `test`, hence the cast).
const { defineConfig, devices } = playwright as unknown as typeof import('@playwright/test')

// The e2e tier runs against the Go binary serving the embedded build and replaying a recorded
// stream, never against `vite dev` (plan Phase 3, GUIDE-testing). `pnpm build` must have run:
// the binary embeds cmd/visual-kubernetes/ui/dist at compile time.
const port = Number(process.env.VK_E2E_PORT ?? 18080)
const fixture = process.env.VK_E2E_FIXTURE ?? 'test/fixtures/streams/deploy-scale.ndjson'

export default defineConfig({
  testDir: '../test/e2e',
  outputDir: '../.agents/scratch/e2e/results',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [['list']],
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    viewport: { width: 1600, height: 900 },
    trace: 'retain-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'], viewport: { width: 1600, height: 900 } } }],
  webServer: {
    command: `go run ./cmd/visual-kubernetes --replay ${fixture} --addr 127.0.0.1:${port}`,
    cwd: '..',
    url: `http://127.0.0.1:${port}/healthz`,
    reuseExistingServer: false,
    timeout: 120_000,
    stdout: 'pipe',
  },
})
