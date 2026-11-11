import playwright from '@playwright/test'
import { SERVERS, urlOf, type ServerName } from '../test/e2e/servers'

// web/package.json is "type": "module", so Node loads this file as native ESM, and
// @playwright/test is CommonJS without static named exports: take them off the default,
// which at runtime is module.exports (its typings declare it as `test`, hence the cast).
const { defineConfig, devices } = playwright as unknown as typeof import('@playwright/test')

// The e2e tier runs against the Go binary serving the embedded build and replaying a recorded
// stream, never against `vite dev` (plan Phase 3, GUIDE-testing). `pnpm build` must have run:
// the binary embeds cmd/visual-kubernetes/ui/dist at compile time. Each recorded stream gets
// its own server (test/e2e/servers.ts); specs pick one with test.use({ baseURL }). The
// telemetry stream is written by test/e2e/fixtures/telemetry.mjs into .agents/scratch/ first.
function server(name: ServerName) {
  const { port, fixture } = SERVERS[name]
  const run = `go run ./cmd/visual-kubernetes --replay ${fixture} --addr 127.0.0.1:${port}`
  const gen =
    name === 'telemetry'
      ? `node test/e2e/fixtures/telemetry.mjs ${fixture} && `
      : name === 'traffic'
        ? `node test/e2e/fixtures/traffic.mjs ${fixture} && `
        : ''
  return {
    command: `${gen}${run}`,
    cwd: '..',
    url: `${urlOf(name)}/healthz`,
    reuseExistingServer: false,
    timeout: 120_000,
    stdout: 'pipe' as const,
  }
}

export default defineConfig({
  testDir: '../test/e2e',
  outputDir: '../.agents/scratch/e2e/results',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [['list']],
  use: {
    baseURL: urlOf('deployScale'),
    viewport: { width: 1600, height: 900 },
    trace: 'retain-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'], viewport: { width: 1600, height: 900 } } }],
  webServer: (Object.keys(SERVERS) as ServerName[]).map(server),
})
