import type { Graph } from '../lib/graph'
import type { Entity } from '../lib/wire'
import { Section } from '../panel/sections'
import { type ExecSession, type ExecShell } from './api'
import { isProtectedNamespace } from './configApi'

type Props = {
  readonly graph: Graph
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

function runningPods(items: readonly Entity[]): Entity[] {
  const seen = new Map<string, Entity>()
  for (const p of items) {
    if (p.kind !== 'Pod' || p.folded || p.status !== 'Running') continue
    if (isProtectedNamespace(p.namespace)) continue
    seen.set(p.id, p)
  }
  return [...seen.values()].sort((a, b) => a.id.localeCompare(b.id))
}

function serviceTargetPods(graph: Graph, service: Entity): Entity[] {
  const appIds = new Set<string>()
  if (service.appId) appIds.add(service.appId)
  for (const edge of graph.edges.values()) {
    if ((edge.kind === 'selects' || edge.kind === 'routes') && edge.from === service.id && edge.to.startsWith('app/')) {
      appIds.add(edge.to)
    }
  }
  if (appIds.size === 0) return []
  const pods: Entity[] = []
  for (const e of graph.entities.values()) {
    if (e.kind === 'Pod' && e.appId && appIds.has(e.appId)) {
      pods.push(e)
    }
  }
  return runningPods(pods)
}

function PodExecChips({
  pods,
  showNamespace,
  note,
  onOpenExec,
}: {
  readonly pods: readonly Entity[]
  readonly showNamespace: boolean
  readonly note: string
  readonly onOpenExec: (session: ExecSession) => void
}) {
  return (
    <Section title="Pod terminal" name="demo-exec">
      <p className="demo-note">{note}</p>
      <div className="chips">
        {pods.map((p) => (
          <button
            type="button"
            key={p.id}
            className="chip"
            data-exec-pod={p.id}
            data-testid={`exec-pod-${p.name}`}
            onClick={() => onOpenExec(makeSession(p, '/bin/sh'))}
          >
            {showNamespace ? `${p.namespace}/${p.name}` : p.name} <span className="muted">/bin/sh</span>
          </button>
        ))}
      </div>
    </Section>
  )
}

export function ExecSection({ graph, entity, role, pods, onOpenExec }: Props) {
  const isNamespacedSubject = role === 'pod' || role === 'app' || (role === 'sat' && entity.kind === 'Service')
  if (isNamespacedSubject && isProtectedNamespace(entity.namespace)) {
    return (
      <Section title="Pod terminal" name="demo-exec">
        <p className="demo-note" data-testid="exec-protected">
          System namespace is read-only in demo mode — pod terminal disabled.
        </p>
      </Section>
    )
  }

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
    const running = runningPods(pods)
    if (running.length === 0) {
      return (
        <Section title="Pod terminal" name="demo-exec">
          <p className="demo-note" data-testid="exec-no-pods">
            No Running pods yet — scale replicas above 0 or wait for readiness.
          </p>
        </Section>
      )
    }
    return (
      <PodExecChips
        pods={running}
        showNamespace={false}
        note="Open an interactive /bin/sh terminal on a running pod."
        onOpenExec={onOpenExec}
      />
    )
  }

  if (role === 'sat' && entity.kind === 'Service') {
    const running = serviceTargetPods(graph, entity)
    if (running.length === 0) {
      return (
        <Section title="Pod terminal" name="demo-exec">
          <p className="demo-note" data-testid="exec-service-empty">
            No Running pods backing this Service yet — scale replicas above 0 or wait for readiness.
          </p>
        </Section>
      )
    }
    return (
      <PodExecChips
        pods={running}
        showNamespace={false}
        note="Open /bin/sh on a running pod backing this Service."
        onOpenExec={onOpenExec}
      />
    )
  }

  if (role === 'node') {
    const nodePods = runningPods(
      [...graph.entities.values()].filter((p) => p.kind === 'Pod' && p.nodeName === entity.name),
    )
    if (nodePods.length === 0) {
      return (
        <Section title="Pod terminal" name="demo-exec">
          <p className="demo-note" data-testid="exec-node-empty">
            No non-system Running pods on this node.
          </p>
        </Section>
      )
    }
    return (
      <PodExecChips
        pods={nodePods}
        showNamespace={true}
        note="Open /bin/sh on a non-system Running pod scheduled on this node."
        onOpenExec={onOpenExec}
      />
    )
  }

  return null
}
