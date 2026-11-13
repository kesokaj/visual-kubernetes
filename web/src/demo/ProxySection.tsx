import type { Entity } from '../lib/wire'
import { Section } from '../panel/sections'
import { type BrowseSession, type DemoPort } from './api'
import { isProtectedNamespace } from './configApi'
import { useDemoPorts } from './useDemoPorts'

type Role = 'hub' | 'cp' | 'node' | 'pod' | 'app' | 'sat' | 'ext'

type Props = {
  entity: Entity
  role: Role
  refreshNonce?: number
  onBrowse: (session: BrowseSession) => void
  onPortClosed: (serviceId: string, port: number) => void
}

function matchesEntity(port: DemoPort, entity: Entity, role: Role): boolean {
  if (role === 'ext') return true
  if (role === 'app') return port.appId === entity.id
  return role === 'sat' && entity.kind === 'Service' && port.id === entity.id
}

export function ProxySection({ entity, role, refreshNonce = 0, onBrowse, onPortClosed }: Props) {
  const eligible = role === 'app' || role === 'ext' || (role === 'sat' && entity.kind === 'Service')
  const enabled = eligible && (role === 'ext' || !isProtectedNamespace(entity.namespace))
  const { ports, busyKey, error, setOpen } = useDemoPorts({
    enabled,
    entityId: entity.id,
    refreshNonce,
    onBrowse,
    onPortClosed,
  })
  if (!enabled) return null
  const visible = ports.filter((p) => matchesEntity(p, entity, role))
  if (visible.length === 0 && !error) return null

  return (
    <Section title="Exposed ports · demo proxy" name="demo-proxy">
      <p className="demo-note">Non-production demo proxy · ports start closed until opened here.</p>
      {error && (
        <p className="demo-err" role="alert">
          {error}
        </p>
      )}
      <ul className="demo-ports">
        {visible.map((p) => (
          <PortRow
            key={`${p.id}:${p.port}`}
            port={p}
            busy={busyKey === `${p.id}:${p.port}`}
            onBrowse={onBrowse}
            onToggle={setOpen}
          />
        ))}
      </ul>
    </Section>
  )
}

function PortRow({
  port: p,
  busy,
  onBrowse,
  onToggle,
}: {
  port: DemoPort
  busy: boolean
  onBrowse: (session: BrowseSession) => void
  onToggle: (port: DemoPort, open: boolean) => Promise<void>
}) {
  const session: BrowseSession = {
    id: p.id,
    namespace: p.namespace,
    service: p.service,
    port: p.port,
    pathPrefix: p.pathPrefix,
  }
  return (
    <li
      className="demo-port-row"
      data-demo-port={`${p.namespace}/${p.service}:${p.port}`}
      data-open={p.open ? 'true' : 'false'}
    >
      <div className="demo-port-meta">
        <b>
          {p.service}:{p.port}
        </b>
        <span className="demo-exp">{p.exposure}</span>
        <span className={`demo-badge ${p.open ? 'open' : 'closed'}`}>{p.open ? 'Open' : 'Closed'}</span>
      </div>
      <div className="demo-port-actions">
        {p.open ? (
          <>
            <button type="button" className="demo-btn primary" data-action="browse-port" disabled={busy} onClick={() => onBrowse(session)}>
              Browse
            </button>
            <button type="button" className="demo-btn danger" data-action="close-port" disabled={busy} onClick={() => void onToggle(p, false)}>
              Close port
            </button>
          </>
        ) : (
          <button type="button" className="demo-btn primary" data-action="open-port" disabled={busy} onClick={() => void onToggle(p, true)}>
            Open in app
          </button>
        )}
      </div>
    </li>
  )
}
