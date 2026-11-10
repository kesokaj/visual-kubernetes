import { measureOf, type Metrics } from '../lib/telemetry'
import type { SizeBy } from '../lib/wire'
import type { Anchor } from './scene'

// App icon sizes in world units: the smallest app keeps a readable icon, the busiest is
// roughly twice the mockup's 44 (PRD S2). Area, not radius, follows the measure.
export const APP_MIN = 34
export const APP_MAX = 80
export const HALO_TOP = 3

// sizeApps sets each app's measure, icon size and top-3 rank from the current sizeBy: network
// (rx + tx) or CPU from the latest metrics, else the app's pod count. podCount counts every
// pod of an app, drawn or not, so hiding the Pods group does not resize the web.
export function sizeApps(apps: Anchor[], metrics: Metrics, podCount: ReadonlyMap<string, number>): SizeBy {
  const sizeBy = metrics.sizeBy
  for (const a of apps) {
    a.measure = sizeBy === 'pods' ? (podCount.get(a.id) ?? 0) : measureOf(metrics.latest.get(a.id), sizeBy)
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
  return sizeBy
}
