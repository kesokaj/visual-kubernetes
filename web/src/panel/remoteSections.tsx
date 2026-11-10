import { useState } from 'react'
import type { Entity } from '../lib/wire'
import { nodeConditions } from './conditions'
import { logsUrl, useText, type Remote } from './remote'
import { Section } from './sections'

function Body({ res, empty }: { res: Remote; empty: string }) {
  if (res.state === 'loading') return <p className="muted">Loading…</p>
  if (res.state === 'error') return <p className="err">Unavailable: {res.text}</p>
  if (res.state === 'idle') return <p className="muted">{empty}</p>
  return null
}

// ConfigSection shows the object's YAML exactly as /api/objects returns it (Secrets are never
// served there; plan Interfaces).
export function ConfigSection({ res, objectId }: { res: Remote; objectId: string | undefined }) {
  return (
    <Section title={objectId ? `Config · ${objectId}` : 'Config'} name="config">
      <Body res={res} empty="This object has no API config." />
      {res.state === 'ok' && <pre className="yaml">{res.text}</pre>}
    </Section>
  )
}

// LogsSection tails a pod's logs. Logs are the application's own output and can carry
// secrets; the README says so, and nothing here stores or forwards them.
export function LogsSection({ podId }: { podId: string | undefined }) {
  const [nonce, setNonce] = useState(0)
  const res = useText(logsUrl(podId), nonce)
  return (
    <Section title={podId ? `Logs · ${podId.split('/').pop() ?? ''}` : 'Logs'} name="logs">
      <div className="p-row">
        <span className="muted">Last 200 lines; logs can contain secrets.</span>
        {podId && (
          <button type="button" className="p-btn" onClick={() => setNonce((n) => n + 1)}>
            Refresh
          </button>
        )}
      </div>
      <Body res={res} empty="No pod to read logs from." />
      {res.state === 'ok' && <pre className="logs">{res.text || '(no output)'}</pre>}
    </Section>
  )
}

// ConditionsSection lists a node's conditions from its YAML; when the YAML has none (replay
// mode serves the entity, not the Node) it falls back to the entity's status.
export function ConditionsSection({ res, node }: { res: Remote; node: Entity }) {
  const conds = res.state === 'ok' ? nodeConditions(res.text) : []
  return (
    <Section title="Conditions" name="conditions">
      {conds.length === 0 ? (
        <p className="muted" data-fallback="status">
          {res.state === 'loading' ? 'Loading…' : `Status: ${node.status || 'unknown'} (no conditions reported)`}
        </p>
      ) : (
        <ul className="conds">
          {conds.map((c) => (
            <li key={c.type} data-condition={c.type} data-status={c.status} className={bad(c.type, c.status) ? 'warn' : undefined}>
              <b>{c.type}</b>
              <span>{c.status}</span>
              <span className="muted">{c.reason}</span>
            </li>
          ))}
        </ul>
      )}
    </Section>
  )
}

// bad marks a condition the audience should notice: Ready not True, or a pressure condition
// that is True.
function bad(type: string, status: string): boolean {
  return type === 'Ready' ? status !== 'True' : status === 'True'
}
