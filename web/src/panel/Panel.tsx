import { useSyncExternalStore } from 'react'
import type { BrowseSession } from '../demo/api'
import { ProxySection } from '../demo/ProxySection'
import { Globe, Icon, iconFor } from '../icons/sprite'
import type { Graph } from '../lib/graph'
import type { Entity } from '../lib/wire'
import { foldedStatics } from '../web/folding'
import { objectUrl, useText } from './remote'
import { ConditionsSection, ConfigSection, LogsSection } from './remoteSections'
import { CapabilitiesSection, EventsSection, KV, MetricsSection, Section } from './sections'

type Role = 'hub' | 'cp' | 'node' | 'pod' | 'app' | 'sat' | 'ext'

function roleOf(e: Entity): Role {
  if (e.id === 'cp/apiserver') return 'hub'
  if (e.group === 'cp') return 'cp'
  if (e.kind === 'Node') return 'node'
  if (e.group === 'ext') return 'ext'
  // app/ first: a bare Pod's own app (D23) has kind Pod too (BUG-001)
  if (e.id.startsWith('app/')) return 'app'
  return e.kind === 'Pod' ? 'pod' : 'sat'
}

// configId is the D13 object /api/objects can serve for a body: an app is shown through its
// top workload; cp/* parts and ext/internet are inferred and have no single object.
function configId(e: Entity, role: Role): string | undefined {
  if (role === 'hub' || role === 'cp' || role === 'ext') return undefined
  return role === 'app' ? e.ownerId : e.id
}

type Props = {
  graph: Graph
  id: string
  onClose: () => void
  onSelect: (id: string) => void
  onBrowse?: (session: BrowseSession) => void
  onPortClosed?: (serviceId: string, port: number) => void
}

// Panel is the click panel: a right-hand sheet with what one object is and does now. Its
// sections depend on the object (plan Phase 4): config, live Events, metrics, logs, node
// conditions, the data sources on the API server. Esc, × or a click on empty space close it.
export function Panel({ graph, id, onClose, onSelect, onBrowse, onPortClosed }: Props) {
  useSyncExternalStore(graph.subscribe, graph.getVersion, graph.getVersion)
  const e = graph.entities.get(id)
  const role = e ? roleOf(e) : 'sat'
  const config = useText(e ? objectUrl(configId(e, role)) : null)
  if (!e) {
    return (
      <aside className="panel" data-testid="panel" data-panel-for={id}>
        <PanelHead title={id.split('/').pop() ?? id} sub="Gone from the cluster" kind="" onClose={onClose} />
      </aside>
    )
  }
  const pods = role === 'app' ? podsOf(graph, e.id) : []
  const demoEnabled = graph.caps['demo']?.state === 'ok'
  return (
    <aside className="panel" data-testid="panel" data-panel-for={id} data-panel-role={role}>
      <PanelHead title={e.name} sub={[e.kind, e.namespace].filter(Boolean).join(' · ')} kind={e.kind} app={role === 'app'} onClose={onClose} />
      <div className="p-body">
        <Summary graph={graph} e={e} role={role} pods={pods} />
        {demoEnabled && onBrowse && onPortClosed && (
          <ProxySection entity={e} role={role} onBrowse={onBrowse} onPortClosed={onPortClosed} />
        )}
        {role === 'hub' && <CapabilitiesSection caps={graph.caps} />}
        {role === 'cp' && <Statics graph={graph} cpId={e.id} />}
        {role === 'node' && <ConditionsSection res={config} node={e} />}
        {(role === 'app' || role === 'pod') && <MetricsSection history={graph.metrics.history.get(e.id) ?? []} caps={graph.caps} />}
        {role === 'app' && <PodChips pods={pods} onSelect={onSelect} />}
        {role === 'ext' && <Entrypoints graph={graph} id={e.id} onSelect={onSelect} />}
        <EventsSection events={graph.events.about(eventIds(e, pods))} />
        {(role === 'app' || role === 'pod') && <LogsSection podId={role === 'pod' ? e.id : pods[0]?.id} />}
        {role !== 'hub' && role !== 'cp' && role !== 'ext' && <ConfigSection res={config} objectId={configId(e, role)} />}
      </div>
    </aside>
  )
}

type HeadProps = { title: string; sub: string; kind: string; app?: boolean; onClose: () => void }

function PanelHead({ title, sub, kind, app = false, onClose }: HeadProps) {
  return (
    <header className="p-head">
      {kind && (
        <svg className="p-icon" viewBox="-20 -20 40 40" aria-hidden="true">
          {kind === 'Internet' ? <Globe size={36} x={0} y={0} /> : <Icon name={iconFor(kind, app)} size={36} x={0} y={0} />}
        </svg>
      )}
      <div className="p-title">
        <h4>{title}</h4>
        <span>{sub}</span>
      </div>
      <button type="button" className="p-close" aria-label="Close panel" onClick={onClose}>
        ×
      </button>
    </header>
  )
}

function podsOf(graph: Graph, appId: string): Entity[] {
  return [...graph.entities.values()].filter((p) => p.kind === 'Pod' && p.appId === appId).sort((a, b) => a.id.localeCompare(b.id))
}

// eventIds are the objects whose Events belong in this panel: the object itself, an app's top
// workload, and its pods with their direct owners (the ReplicaSet scaling them).
function eventIds(e: Entity, pods: readonly Entity[]): Set<string> {
  const ids = new Set([e.id])
  if (e.ownerId) ids.add(e.ownerId)
  for (const p of pods) {
    ids.add(p.id)
    if (p.ownerId) ids.add(p.ownerId)
  }
  return ids
}

function Summary({ graph, e, role, pods }: { graph: Graph; e: Entity; role: Role; pods: readonly Entity[] }) {
  const rows: [string, string][] = [['Status', e.status]]
  if (role === 'app') rows.push(['Pods', `${pods.length}`], ['Workload', e.ownerId ?? ''])
  if (role === 'pod') rows.push(['Node', e.nodeName ?? 'not scheduled'], ['App', e.appId ?? ''])
  if (role === 'node') {
    const n = [...graph.entities.values()].filter((p) => p.kind === 'Pod' && p.nodeName === e.name).length
    rows.push(['Pods', `${n}`])
  }
  if (role === 'cp') rows.push(['Evidence', e.labels['visual-kubernetes/evidence'] ?? 'none'])
  return (
    <Section title={role === 'cp' ? 'Inferred control-plane part' : 'Summary'} name="summary">
      <KV rows={rows} />
    </Section>
  )
}

function PodChips({ pods, onSelect }: { pods: readonly Entity[]; onSelect: (id: string) => void }) {
  return (
    <Section title={`Pods · ${pods.length}`} name="pods">
      <div className="chips">
        {pods.map((p) => (
          <button type="button" key={p.id} className="chip" data-pod={p.id} onClick={() => onSelect(p.id)}>
            {p.name} <span className="muted">{p.status}</span>
          </button>
        ))}
      </div>
    </Section>
  )
}

// Entrypoints lists what the internet reaches (its routes edges: LoadBalancer/NodePort
// Services, Ingresses, Gateways or their apps); a chip selects one.
function Entrypoints({ graph, id, onSelect }: { graph: Graph; id: string; onSelect: (id: string) => void }) {
  const targets = [...graph.edges.values()]
    .filter((x) => x.from === id && x.kind === 'routes')
    .flatMap((x) => graph.entities.get(x.to) ?? [])
    .sort((a, b) => a.id.localeCompare(b.id))
  return (
    <Section title={`Entrypoints · ${targets.length}`} name="entrypoints">
      <div className="chips">
        {targets.map((t) => (
          <button type="button" key={t.id} className="chip" data-entrypoint={t.id} onClick={() => onSelect(t.id)}>
            {t.name} <span className="muted">{t.kind}</span>
          </button>
        ))}
      </div>
    </Section>
  )
}

// Statics lists the static control-plane pods folded behind this cp/* part (plan D30).
function Statics({ graph, cpId }: { graph: Graph; cpId: string }) {
  const mine = (foldedStatics(graph.entities).get(cpId) ?? []).flatMap((id) => graph.entities.get(id) ?? [])
  if (mine.length === 0) return null
  return (
    <Section title="Static pods folded here" name="statics">
      <ul className="plain">
        {mine.map((a) => (
          <li key={a.id} data-static={a.id}>
            {a.name} <span className="muted">{a.status}</span>
          </li>
        ))}
      </ul>
    </Section>
  )
}
