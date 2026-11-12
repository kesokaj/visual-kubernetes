import type { Entity } from '../lib/wire'
import { Section } from '../panel/sections'
import { type ExecSession, type ExecShell, isProtectedNamespace } from './api'

type Props = {
  readonly entity: Entity
  readonly role: string
  readonly pods: readonly Entity[]
  readonly onOpenExec: (session: ExecSession) => void
}

function makeSession(pod: Entity, shell: ExecShell): ExecSession {
  return {
    podId: pod.id,
    namespace: pod.namespace,
    pod: pod.name,
    shell,
  }
}

export function ExecSection({ entity, role, pods, onOpenExec }: Props) {
  if (isProtectedNamespace(entity.namespace)) return null

  if (role === 'pod') {
    if (entity.status !== 'Running') {
      return (
        <Section title="Pod terminal" name="demo-exec">
          <p className="demo-note" data-testid="exec-not-running">
            Pod must be Running to open an interactive shell ({entity.status}).
          </p>
        </Section>
      )
    }
    return (
      <Section title="Pod terminal" name="demo-exec">
        <p className="demo-note">Interactive shell inside this pod (non-production demo).</p>
        <div className="demo-exec-launch">
          <button
            type="button"
            className="demo-btn primary"
            data-testid="exec-open-sh"
            onClick={() => onOpenExec(makeSession(entity, '/bin/sh'))}
          >
            Open /bin/sh
          </button>
          <button
            type="button"
            className="demo-btn"
            data-testid="exec-open-bash"
            onClick={() => onOpenExec(makeSession(entity, '/bin/bash'))}
          >
            Open /bin/bash
          </button>
        </div>
      </Section>
    )
  }

  if (role === 'app') {
    const running = pods.filter((p) => p.status === 'Running' && !isProtectedNamespace(p.namespace))
    if (running.length === 0) return null
    return (
      <Section title="Pod terminal" name="demo-exec">
        <p className="demo-note">Open an interactive /bin/sh terminal on a running pod.</p>
        <div className="chips">
          {running.map((p) => (
            <button
              type="button"
              key={p.id}
              className="chip"
              data-exec-pod={p.id}
              data-testid={`exec-pod-${p.name}`}
              onClick={() => onOpenExec(makeSession(p, '/bin/sh'))}
            >
              {p.name} <span className="muted">/bin/sh</span>
            </button>
          ))}
        </div>
      </Section>
    )
  }

  return null
}
