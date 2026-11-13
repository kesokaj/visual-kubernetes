import type { Graph } from '../lib/graph'
import type { Entity } from '../lib/wire'
import { Section } from '../panel/sections'
import {
  findAutoscalersForEntity,
  isProtectedNamespace,
  type HPASpecPayload,
  type VPASpecPayload,
} from './configApi'
import { useAutoscaler, type HPADraft, type VPADraft } from './useAutoscaler'

type Props = {
  readonly graph: Graph
  readonly entity: Entity
  readonly role: string
}

export function AutoscalerSection({ graph, entity, role }: Props) {
  const autoscalers = findAutoscalersForEntity(graph, entity, role)
  if (autoscalers.length === 0) return null

  const hasProtected =
    isProtectedNamespace(entity.namespace) || autoscalers.some((a) => isProtectedNamespace(a.namespace))
  if (hasProtected) {
    return (
      <Section title="Autoscaling" name="demo-autoscaler">
        <p className="demo-note" data-testid="autoscaler-readonly">
          System namespace autoscalers are read-only in demo mode.
        </p>
      </Section>
    )
  }

  return (
    <Section title="Autoscaling" name="demo-autoscaler">
      <div className="demo-card-stack">
        {autoscalers.map((a) => (
          <AutoscalerCard key={a.id} entity={a} />
        ))}
      </div>
    </Section>
  )
}

function AutoscalerCard({ entity }: { readonly entity: Entity }) {
  const st = useAutoscaler(entity.id)
  const payload = st.payload
  const badge = entity.kind === 'HorizontalPodAutoscaler' ? 'HPA' : 'VPA'

  return (
    <div className="demo-card" data-autoscaler-card={entity.id}>
      <div className="demo-card-head">
        <b>{entity.name}</b>
        <span className="demo-badge closed">{badge}</span>
      </div>

      {st.loading && !payload && (
        <p className="demo-note" data-testid="autoscaler-loading">
          Loading {badge} configuration…
        </p>
      )}

      {st.notFound && (
        <p className="demo-note" data-testid="autoscaler-missing">
          {st.error ?? `${badge} resource or CRD is not installed on this cluster.`}
        </p>
      )}

      {st.conflict && (
        <div className="demo-conflict" role="alert" data-testid="autoscaler-conflict">
          <span>{st.error ?? 'Autoscaler changed on the cluster.'}</span>
          <button
            type="button"
            className="demo-btn"
            data-testid="autoscaler-reload"
            onClick={st.reload}
          >
            Reload
          </button>
        </div>
      )}

      {((st.error && !st.conflict && !st.notFound) || st.validationError) && (
        <p className="demo-err" role="alert" data-testid="autoscaler-error">
          {(st.conflict || st.notFound ? null : st.error) ?? st.validationError}
        </p>
      )}

      {st.notice && (
        <p className="demo-ok" role="status" data-testid="autoscaler-saved">
          {st.notice}
        </p>
      )}

      {payload?.kind === 'HorizontalPodAutoscaler' && payload.hpa && st.hpaDraft && (
        <HPAForm
          spec={payload.hpa}
          draft={st.hpaDraft}
          busy={st.busy}
          onChange={st.setHpaField}
        />
      )}

      {payload?.kind === 'VerticalPodAutoscaler' && payload.vpa && st.vpaDraft && (
        <VPAForm
          spec={payload.vpa}
          draft={st.vpaDraft}
          busy={st.busy}
          onModeChange={st.setVpaMode}
          onContainerChange={st.setVpaContainer}
        />
      )}

      {payload && (
        <div className="demo-env-actions">
          {st.dirty && (
            <button
              type="button"
              className="demo-btn"
              disabled={st.busy}
              data-testid="autoscaler-reset"
              onClick={st.reset}
            >
              Reset
            </button>
          )}
          <button
            type="button"
            className="demo-btn primary"
            disabled={st.busy || !st.dirty || st.validationError !== null}
            data-testid="autoscaler-save"
            onClick={() => void st.save()}
          >
            {st.saving ? 'Saving…' : 'Apply'}
          </button>
        </div>
      )}
    </div>
  )
}

function HPAForm({
  spec,
  draft,
  busy,
  onChange,
}: {
  readonly spec: HPASpecPayload
  readonly draft: HPADraft
  readonly busy: boolean
  readonly onChange: (field: 'min' | 'max', value: string) => void
}) {
  return (
    <div className="demo-auto-body">
      <p className="demo-exp" data-testid="hpa-current-replicas">
        Horizontal pod scaling · current replicas: {spec.currentReplicas}
      </p>
      <div className="demo-field-grid">
        <label className="demo-field">
          <span>Min replicas</span>
          <input
            type="number"
            min={1}
            max={50}
            className="demo-env-input"
            data-testid="hpa-min-input"
            value={draft.min}
            disabled={busy}
            onChange={(e) => onChange('min', e.target.value)}
          />
        </label>
        <label className="demo-field">
          <span>Max replicas</span>
          <input
            type="number"
            min={1}
            max={50}
            className="demo-env-input"
            data-testid="hpa-max-input"
            value={draft.max}
            disabled={busy}
            onChange={(e) => onChange('max', e.target.value)}
          />
        </label>
      </div>
    </div>
  )
}

function VPAForm({
  spec,
  draft,
  busy,
  onModeChange,
  onContainerChange,
}: {
  readonly spec: VPASpecPayload
  readonly draft: VPADraft
  readonly busy: boolean
  readonly onModeChange: (mode: string) => void
  readonly onContainerChange: (name: string, patch: { maxCPU?: string; maxMemory?: string }) => void
}) {
  return (
    <div className="demo-auto-body">
      <p className="demo-note" data-testid="vpa-vertical-note">
        VPA resizes container CPU and memory vertically — it does not scale pod count.
      </p>
      <label className="demo-field">
        <span>Update mode</span>
        <select
          className="demo-env-input"
          data-testid="vpa-mode-select"
          value={draft.updateMode}
          disabled={busy}
          onChange={(e) => onModeChange(e.target.value)}
        >
          {spec.allowedModes.map((mode) => (
            <option key={mode} value={mode}>
              {mode}
            </option>
          ))}
        </select>
      </label>
      <div className="demo-vpa-list">
        {draft.containers.map((c) => (
          <div key={c.containerName} className="demo-vpa-row" data-vpa-container={c.containerName}>
            <span className="demo-env-key">{c.containerName}</span>
            <div className="demo-field-grid">
              <label className="demo-field">
                <span>Max CPU</span>
                <input
                  type="text"
                  className="demo-env-input"
                  placeholder="unset (e.g. 500m)"
                  data-testid={`vpa-max-cpu-${c.containerName}`}
                  value={c.maxCPU}
                  disabled={busy}
                  onChange={(e) => onContainerChange(c.containerName, { maxCPU: e.target.value })}
                />
              </label>
              <label className="demo-field">
                <span>Max Memory</span>
                <input
                  type="text"
                  className="demo-env-input"
                  placeholder="unset (e.g. 256Mi)"
                  data-testid={`vpa-max-memory-${c.containerName}`}
                  value={c.maxMemory}
                  disabled={busy}
                  onChange={(e) => onContainerChange(c.containerName, { maxMemory: e.target.value })}
                />
              </label>
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
