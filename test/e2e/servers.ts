// One replay server per recorded stream (web/playwright.config.ts starts them all). A replay
// plays once from its first subscriber, and a later page gets only the snapshot (no deltas, no
// chains), so a spec that watches deltas happen must be the first to open its server:
// node-join and missing-sources are only opened by encodings.spec.ts, deployScaleEvents
// (deploy-scale again, for its Kubernetes Events) only by panel.spec.ts, deployScaleReplay
// (deploy-scale again, for its causal chain) only by replay.spec.ts, and g3Scale (one G3-sized
// snapshot) by perf.spec.ts. telemetry repeats its metrics and Events for the whole run.
export const SERVERS = {
  deployScale: { port: 18080, fixture: 'test/fixtures/streams/deploy-scale.ndjson' },
  nodeJoin: { port: 18081, fixture: 'test/fixtures/streams/node-join.ndjson' },
  missingSources: { port: 18082, fixture: 'test/fixtures/streams/missing-sources.ndjson' },
  largeCluster: { port: 18083, fixture: 'test/fixtures/streams/large-cluster.ndjson' },
  telemetry: { port: 18084, fixture: '.agents/scratch/e2e/telemetry.ndjson' },
  deployScaleEvents: { port: 18085, fixture: 'test/fixtures/streams/deploy-scale.ndjson' },
  deployScaleReplay: { port: 18086, fixture: 'test/fixtures/streams/deploy-scale.ndjson' },
  g3Scale: { port: 18087, fixture: 'test/fixtures/streams/g3-scale.ndjson' },
} as const

export type ServerName = keyof typeof SERVERS

export const urlOf = (name: ServerName) => `http://127.0.0.1:${SERVERS[name].port}`
