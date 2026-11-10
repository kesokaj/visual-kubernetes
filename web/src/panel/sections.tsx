import type { ReactNode } from 'react'
import type { Capabilities, K8sEvent, Sample } from '../lib/wire'
import { bytesRate, clock, cpu, memory } from './format'

export function Section({ title, children, name }: { title: string; children: ReactNode; name: string }) {
  return (
    <section className="p-sec" data-section={name}>
      <h5>{title}</h5>
      {children}
    </section>
  )
}

export function KV({ rows }: { rows: readonly (readonly [string, ReactNode])[] }) {
  return (
    <dl className="kv">
      {rows.map(([k, v]) => (
        <div key={k}>
          <dt>{k}</dt>
          <dd>{v === '' || v === undefined ? '—' : v}</dd>
        </div>
      ))}
    </dl>
  )
}

// Sparkline draws one series over the last 90 s (viewBox 0 0 100 34, as in the mockup).
function Sparkline({ values }: { values: readonly number[] }) {
  const max = Math.max(1, ...values)
  const step = values.length > 1 ? 100 / (values.length - 1) : 100
  const points = values.map((v, i) => `${(i * step).toFixed(1)},${(32 - (v / max) * 30).toFixed(1)}`).join(' ')
  return (
    <svg className="spark" viewBox="0 0 100 34" preserveAspectRatio="none" aria-hidden="true">
      <polyline points={values.length > 1 ? points : `0,32 100,32`} />
    </svg>
  )
}

type Series = { name: string; label: string; values: number[]; show: (v: number) => string }

function seriesOf(history: readonly Sample[]): Series[] {
  const net = history.map((s) => (s.rxBps ?? 0) + (s.txBps ?? 0))
  const out: Series[] = []
  if (history.some((s) => s.rxBps !== undefined || s.txBps !== undefined)) {
    out.push({ name: 'network', label: 'Network rx+tx', values: net, show: bytesRate })
  }
  if (history.some((s) => s.cpuMilli !== undefined)) {
    out.push({ name: 'cpu', label: 'CPU', values: history.map((s) => s.cpuMilli ?? 0), show: cpu })
  }
  if (history.some((s) => s.memBytes !== undefined)) {
    out.push({ name: 'memory', label: 'Memory', values: history.map((s) => s.memBytes ?? 0), show: memory })
  }
  return out
}

// MetricsSection shows a sparkline per measure the samples carry; without samples it says
// which source is missing instead of drawing flat lines.
export function MetricsSection({ history, caps }: { history: readonly Sample[]; caps: Capabilities }) {
  const series = seriesOf(history)
  const src = caps['metrics.k8s.io']
  return (
    <Section title="Metrics · last 90 s" name="metrics">
      {series.length === 0 ? (
        <p className="muted">No metrics yet{src && src.state !== 'ok' ? ` (metrics.k8s.io ${src.state})` : ''}.</p>
      ) : (
        series.map((s) => (
          <div className="met" key={s.name} data-metric={s.name}>
            <div className="met-head">
              <span>{s.label}</span>
              <b>{s.show(s.values[s.values.length - 1] ?? 0)}</b>
            </div>
            <Sparkline values={s.values} />
          </div>
        ))
      )}
    </Section>
  )
}

export function EventsSection({ events }: { events: readonly K8sEvent[] }) {
  return (
    <Section title="Events" name="events">
      {events.length === 0 ? (
        <p className="muted">No recent events.</p>
      ) : (
        <ul className="events">
          {events.map((e) => (
            <li key={`${e.at}|${e.reason}|${e.message}`} className={e.type === 'Warning' ? 'warn' : undefined} data-reason={e.reason}>
              <span className="ev-time">{clock(e.at)}</span>
              <b>{e.reason}</b>
              <span>{e.message}</span>
            </li>
          ))}
        </ul>
      )}
    </Section>
  )
}

// CapabilitiesSection lists every data source and its probe state on the API server's panel.
export function CapabilitiesSection({ caps }: { caps: Capabilities }) {
  const names = Object.keys(caps).sort()
  return (
    <Section title="Data sources" name="capabilities">
      {names.length === 0 ? (
        <p className="muted">No capability report yet.</p>
      ) : (
        <ul className="caps">
          {names.map((n) => (
            <li key={n} data-cap={n} data-state={caps[n]?.state}>
              <span className={`cap-dot ${caps[n]?.state ?? ''}`} />
              <b>{n}</b>
              <span className="cap-state">{caps[n]?.state}</span>
              <span className="muted">{caps[n]?.detail}</span>
            </li>
          ))}
        </ul>
      )}
    </Section>
  )
}
