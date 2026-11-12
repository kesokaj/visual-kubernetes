import type { Entity } from '../lib/wire'
import { Section } from '../panel/sections'
import { editableWorkloadId, type WorkloadContainerEnv } from './api'
import { useWorkloadEnv, type DraftEnvRow, type ReadOnlyEnvRow } from './useWorkloadEnv'

type Props = {
  entity: Entity
}

export function EnvSection({ entity }: Props) {
  const workloadId = editableWorkloadId(entity)
  const state = useWorkloadEnv(workloadId)

  if (!workloadId) return null

  return (
    <Section title="Environment · demo editor" name="demo-env">
      <p className="demo-note">
        Non-production demo editor · updates plain container variables; Secret and ConfigMap refs stay read-only.
      </p>
      <ContainerTabs
        containers={state.containers}
        activeName={state.activeContainerName}
        disabled={state.busy}
        onSelect={state.selectContainer}
      />
      <EnvStatusBanners
        conflict={state.conflict}
        error={state.error}
        savedNotice={state.savedNotice}
        dirty={state.dirty}
        busy={state.busy}
        onReload={state.reload}
      />
      {state.loading && state.containers.length === 0 ? (
        <p className="muted">Loading container environment…</p>
      ) : (
        <>
          <EnvList
            drafts={state.drafts}
            readOnlyVars={state.readOnlyVars}
            disabled={state.busy}
            onUpdate={state.updateDraft}
            onRemove={state.removeDraft}
          />
          <div className="demo-env-actions">
            <button type="button" className="demo-btn" data-action="add-env" disabled={state.busy} onClick={state.addDraft}>
              + Add variable
            </button>
            {state.dirty && (
              <button type="button" className="demo-btn" data-action="reset-env" disabled={state.busy} onClick={state.reset}>
                Reset
              </button>
            )}
            <button
              type="button"
              className="demo-btn primary"
              data-action="save-env"
              disabled={state.busy || !state.dirty || state.conflict}
              onClick={() => void state.save()}
            >
              {state.saving ? 'Saving…' : 'Save changes'}
            </button>
          </div>
        </>
      )}
    </Section>
  )
}

function ContainerTabs({
  containers,
  activeName,
  disabled,
  onSelect,
}: {
  containers: readonly WorkloadContainerEnv[]
  activeName: string
  disabled: boolean
  onSelect: (name: string) => void
}) {
  if (containers.length <= 1) return null
  return (
    <div className="chips demo-env-containers">
      {containers.map((c) => (
        <button
          type="button"
          key={c.name}
          className={`chip ${c.name === activeName ? 'active' : ''}`}
          data-container={c.name}
          disabled={disabled}
          onClick={() => onSelect(c.name)}
        >
          {c.name}
        </button>
      ))}
    </div>
  )
}

function EnvStatusBanners({
  conflict,
  error,
  savedNotice,
  dirty,
  busy,
  onReload,
}: {
  conflict: boolean
  error: string | null
  savedNotice: string | null
  dirty: boolean
  busy: boolean
  onReload: () => void
}) {
  return (
    <>
      {conflict && (
        <div className="demo-conflict" role="alert" data-testid="demo-env-conflict">
          <span>{error ?? 'Workload changed since it was loaded; reload and try again.'}</span>
          <button type="button" className="demo-btn primary" data-action="reload-env" disabled={busy} onClick={onReload}>
            Reload
          </button>
        </div>
      )}
      {error && !conflict && (
        <p className="demo-err" role="alert" data-testid="demo-env-error">
          {error}
        </p>
      )}
      {savedNotice && !dirty && !error && (
        <p className="demo-ok" role="status" data-testid="demo-env-saved">
          {savedNotice}
        </p>
      )}
    </>
  )
}

function EnvList({
  drafts,
  readOnlyVars,
  disabled,
  onUpdate,
  onRemove,
}: {
  drafts: readonly DraftEnvRow[]
  readOnlyVars: readonly ReadOnlyEnvRow[]
  disabled: boolean
  onUpdate: (rowId: string, patch: { name?: string; value?: string }) => void
  onRemove: (rowId: string) => void
}) {
  if (drafts.length === 0 && readOnlyVars.length === 0) {
    return <p className="muted demo-env-empty">No container environment variables set yet.</p>
  }
  return (
    <ul className="demo-env-list">
      {drafts.map((row) => (
        <DraftRowItem key={row.rowId} row={row} disabled={disabled} onUpdate={onUpdate} onRemove={onRemove} />
      ))}
      {readOnlyVars.map((ro) => (
        <li key={`ro:${ro.name}`} className="demo-env-row locked" data-env-locked={ro.name}>
          <span className="demo-env-key">{ro.name}</span>
          <span className="demo-badge closed" data-readonly-source={ro.readOnlySource}>
            Locked · {ro.readOnlySource}
          </span>
        </li>
      ))}
    </ul>
  )
}

function DraftRowItem({
  row,
  disabled,
  onUpdate,
  onRemove,
}: {
  row: DraftEnvRow
  disabled: boolean
  onUpdate: (rowId: string, patch: { name?: string; value?: string }) => void
  onRemove: (rowId: string) => void
}) {
  return (
    <li className="demo-env-row" data-env-name={row.isNew ? undefined : row.name} data-env-new={row.isNew ? 'true' : undefined}>
      {row.isNew ? (
        <input
          type="text"
          className="demo-env-input name"
          data-field="env-name"
          aria-label="New variable name"
          placeholder="VAR_NAME"
          value={row.name}
          disabled={disabled}
          onChange={(ev) => onUpdate(row.rowId, { name: ev.target.value })}
        />
      ) : (
        <span className="demo-env-key">{row.name}</span>
      )}
      <input
        type="text"
        className="demo-env-input"
        data-field="env-value"
        aria-label={row.name ? `Value for ${row.name}` : 'New variable value'}
        placeholder="value"
        value={row.value}
        disabled={disabled}
        onChange={(ev) => onUpdate(row.rowId, { value: ev.target.value })}
      />
      <button
        type="button"
        className="demo-btn danger"
        data-action="remove-env"
        aria-label={row.name ? `Remove ${row.name}` : 'Remove variable'}
        disabled={disabled}
        onClick={() => onRemove(row.rowId)}
      >
        Remove
      </button>
    </li>
  )
}
