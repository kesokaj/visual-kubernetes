import playwright from '@playwright/test'
import { SERVERS, urlOf, type ServerName, type ServerSpec } from '../test/e2e/servers'

// web/package.json is "type": "module", so Node loads this file as native ESM, and
// @playwright/test is CommonJS without static named exports: take them off the default,
// which at runtime is module.exports (its typings declare it as `test`, hence the cast).
const { defineConfig, devices } = playwright as unknown as typeof import('@playwright/test')

// When VK_LIVE_URL is set by test/e2e/live.sh, Playwright runs only live.spec.ts against the
// port-forwarded in-cluster Deployment without starting the recorded --replay servers.
const liveUrl = process.env.VK_LIVE_URL

// The e2e tier runs against the Go binary serving the embedded build and replaying a recorded
// stream, never against `vite dev` (plan Phase 3, GUIDE-testing). `pnpm build` must have run:
// the binary embeds cmd/visual-kubernetes/ui/dist at compile time. Each recorded stream gets
// its own server (test/e2e/servers.ts); specs pick one with test.use({ baseURL }). The
// telemetry stream is written by test/e2e/fixtures/telemetry.mjs into .agents/scratch/ first.
function server(name: ServerName) {
  const spec: ServerSpec = SERVERS[name]
  const extra = spec.args ? ` ${spec.args}` : ''
  const run = `go run ./cmd/visual-kubernetes --replay ${spec.fixture} --addr 127.0.0.1:${spec.port}${extra}`
  const gen =
    name === 'telemetry'
      ? `node test/e2e/fixtures/telemetry.mjs ${spec.fixture} && `
      : name === 'traffic'
        ? `node test/e2e/fixtures/traffic.mjs ${spec.fixture} && `
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
  ...(liveUrl ? { testMatch: ['**/live.spec.ts'] } : { testIgnore: ['**/live.spec.ts'] }),
  outputDir: '../.agents/scratch/e2e/results',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [['list']],
  use: {
    baseURL: liveUrl ?? urlOf('deployScale'),
    viewport: { width: 1600, height: 900 },
    trace: 'retain-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'], viewport: { width: 1600, height: 900 } } }],
  ...(liveUrl ? {} : { webServer: (Object.keys(SERVERS) as ServerName[]).map(server) }),
})
