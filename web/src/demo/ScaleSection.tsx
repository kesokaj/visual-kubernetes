import type { Entity } from '../lib/wire'
import { Section } from '../panel/sections'
import { findScalableWorkload } from './configApi'
import { useWorkloadScale } from './useWorkloadScale'

type Props = {
  readonly entity: Entity
  readonly role: string
}

export function ScaleSection({ entity, role }: Props) {
  const info = findScalableWorkload(entity, role)
  if (!info) return null

  if (info.readOnlyReason) {
    return (
      <Section title="Workload scaling · replicas" name="demo-scale">
        <p className="demo-note" data-testid="scale-readonly">
          {info.readOnlyReason}
        </p>
      </Section>
    )
  }

  return <ScaleEditor workloadId={info.id} />
}

function ScaleEditor({ workloadId }: { readonly workloadId: string }) {
  const st = useWorkloadScale(workloadId)
  const payload = st.payload

  return (
    <Section title="Workload scaling · replicas" name="demo-scale">
      <p className="demo-note">Scale Deployment or StatefulSet replicas between {st.min} and {st.max}.</p>

      {st.loading && !payload && (
        <p className="demo-note" data-testid="scale-loading">
          Loading workload scale…
        </p>
      )}

      {st.conflict && (
        <div className="demo-conflict" role="alert" data-testid="scale-conflict">
          <span>{st.error ?? 'Workload changed on the cluster.'}</span>
          <button
            type="button"
            className="demo-btn"
            data-testid="scale-reload"
            onClick={st.reload}
          >
            Reload
          </button>
        </div>
      )}

      {((st.error && !st.conflict) || (payload && st.validationError)) && (
        <p className="demo-err" role="alert" data-testid="scale-error">
          {(st.conflict ? null : st.error) ?? st.validationError}
        </p>
      )}

      {st.notice && (
        <p className="demo-ok" role="status" data-testid="scale-saved">
          {st.notice}
        </p>
      )}

      {payload && (
        <div className="demo-card" data-scale-workload={payload.id}>
          {payload.managedByHPA && (
            <p className="demo-conflict" role="status" data-testid="scale-hpa-warning">
              HPA &ldquo;{payload.managedByHPA}&rdquo; actively manages replicas here and may override manual changes.
            </p>
          )}
          <div className="demo-scale-row">
            <div className="demo-stepper">
              <button
                type="button"
                className="demo-btn"
                aria-label="Decrease replicas"
                data-testid="scale-dec"
                disabled={st.busy || (st.parsedValue !== null && st.parsedValue <= st.min)}
                onClick={() => st.step(-1)}
              >
                −
              </button>
              <input
                type="number"
                min={st.min}
                max={st.max}
                step={1}
                className="demo-env-input demo-scale-input"
                aria-label="Desired replicas"
                data-testid="scale-input"
                value={st.draft}
                disabled={st.busy}
                onChange={(e) => st.setDraft(e.target.value)}
              />
              <button
                type="button"
                className="demo-btn"
                aria-label="Increase replicas"
                data-testid="scale-inc"
                disabled={st.busy || (st.parsedValue !== null && st.parsedValue >= st.max)}
                onClick={() => st.step(1)}
              >
                +
              </button>
            </div>
            <div className="demo-env-actions">
              {st.dirty && (
                <button
                  type="button"
                  className="demo-btn"
                  disabled={st.busy}
                  data-testid="scale-reset"
                  onClick={st.reset}
                >
                  Reset
                </button>
              )}
              <button
                type="button"
                className="demo-btn primary"
                disabled={st.busy || !st.dirty || st.validationError !== null}
                data-testid="scale-save"
                onClick={() => void st.save()}
              >
                {st.saving ? 'Saving…' : 'Apply'}
              </button>
            </div>
          </div>
          <p className="demo-exp" data-testid="scale-current-meta">
            {payload.kind} current spec: {payload.replicas} · rv {payload.resourceVersion}
          </p>
        </div>
      )}
    </Section>
  )
}
