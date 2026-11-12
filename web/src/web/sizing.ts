import type { SizeChoice } from '../filter/state'
import { measureOf, type Metrics, type SizeMetric } from '../lib/telemetry'
import type { Anchor } from './scene'

// App icon sizes in world units: the smallest app keeps a readable icon, the busiest is
// roughly twice the mockup's 44 (PRD S2). Area, not radius, follows the measure.
export const APP_MIN = 34
export const APP_MAX = 80
export const HALO_TOP = 3

// resolveSize turns the presenter's size switch into the metric in use: auto follows the
// server's sizeBy (network, else CPU, else pods; PRD S2), anything else is pinned (PRD C3).
export function resolveSize(choice: SizeChoice, metrics: Metrics): SizeMetric {
  return choice === 'auto' ? metrics.sizeBy : choice
}

// sizeApps sets each app's measure, icon size and top-3 rank from the chosen metric: network
// (rx + tx), CPU or memory from the latest metrics, else the app's pod count. A pinned metric
// the cluster does not report sizes every app alike and ranks none. podCount counts every pod
// of an app, drawn or not, so hiding the Pods group does not resize the web.
export function sizeApps(
  apps: Anchor[],
  metrics: Metrics,
  podCount: ReadonlyMap<string, number>,
  choice: SizeChoice,
): SizeMetric {
  const metric = resolveSize(choice, metrics)
  for (const a of apps) {
    a.measure = metric === 'pods' ? (podCount.get(a.id) ?? 0) : measureOf(metrics.latest.get(a.id), metric)
  }
  const max = apps.reduce((m, a) => Math.max(m, a.measure), 0)
  for (const a of apps) {
    a.icon = max > 0 ? Math.round(APP_MIN + (APP_MAX - APP_MIN) * Math.sqrt(a.measure / max)) : 44
    a.top = 0
  }
  const ranked = apps.filter((a) => a.measure > 0).sort((x, y) => y.measure - x.measure || x.id.localeCompare(y.id))
  ranked.slice(0, HALO_TOP).forEach((a, i) => {
    a.top = i + 1
  })
  return metric
}
