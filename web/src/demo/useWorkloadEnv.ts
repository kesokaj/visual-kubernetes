import { useEffect, useRef, useState } from 'react'
import {
  WorkloadEnvHttpError,
  fetchWorkloadEnv,
  isValidEnvName,
  patchWorkloadEnv,
  type WorkloadContainerEnv,
  type WorkloadEnvPayload,
} from './api'

export type DraftEnvRow = {
  readonly rowId: string
  readonly name: string
  readonly value: string
  readonly isNew: boolean
}

export type ReadOnlyEnvRow = {
  readonly name: string
  readonly readOnlySource: string
}

function buildDrafts(container: WorkloadContainerEnv | undefined): DraftEnvRow[] {
  if (!container) return []
  const out: DraftEnvRow[] = []
  for (const item of container.env) {
    if (item.kind === 'plain') {
      out.push({ rowId: `saved:${item.name}`, name: item.name, value: item.value, isNew: false })
    }
  }
  return out
}

function extractReadOnly(container: WorkloadContainerEnv | undefined): ReadOnlyEnvRow[] {
  if (!container) return []
  const out: ReadOnlyEnvRow[] = []
  for (const item of container.env) {
    if (item.kind === 'readOnly') {
      out.push({ name: item.name, readOnlySource: item.readOnlySource })
    }
  }
  return out
}

function isDirty(drafts: readonly DraftEnvRow[], container: WorkloadContainerEnv | undefined): boolean {
  const base = buildDrafts(container)
  if (drafts.length !== base.length) return true
  for (let i = 0; i < drafts.length; i++) {
    const d = drafts[i]
    const b = base[i]
    if (!d || !b || d.name !== b.name || d.value !== b.value || d.isNew !== b.isNew) return true
  }
  return false
}

function validateDrafts(drafts: readonly DraftEnvRow[], readOnly: readonly ReadOnlyEnvRow[]): string | null {
  if (drafts.length > 128) return 'At most 128 plain environment variables are allowed.'
  const locked = new Set(readOnly.map((r) => r.name))
  const seen = new Set<string>()
  for (const d of drafts) {
    if (!isValidEnvName(d.name)) {
      return `Invalid variable name "${d.name || '(empty)'}" (use letters, digits, or _, starting with a letter or _).`
    }
    if (locked.has(d.name)) {
      return `Variable "${d.name}" is backed by valueFrom and cannot be overwritten.`
    }
    if (seen.has(d.name)) {
      return `Duplicate variable name "${d.name}".`
    }
    seen.add(d.name)
  }
  return null
}

type EnvEditorState = {
  readonly payload: WorkloadEnvPayload | null
  readonly selectedContainer: string
  readonly drafts: readonly DraftEnvRow[]
  readonly loading: boolean
  readonly saving: boolean
  readonly conflict: boolean
  readonly error: string | null
  readonly savedNotice: string | null
}

const initialState: EnvEditorState = {
  payload: null,
  selectedContainer: '',
  drafts: [],
  loading: false,
  saving: false,
  conflict: false,
  error: null,
  savedNotice: null,
}

function useFetchWorkloadEnv(
  workloadId: string | undefined,
  reloadNonce: number,
  selectedRef: { readonly current: string },
  setState: React.Dispatch<React.SetStateAction<EnvEditorState>>,
) {
  useEffect(() => {
    if (!workloadId) {
      setState(initialState)
      return
    }
    const ctrl = new AbortController()
    setState((cur) => ({ ...cur, loading: true, conflict: false, error: null }))
    fetchWorkloadEnv(workloadId, ctrl.signal)
      .then((res) => {
        if (ctrl.signal.aborted) return
        const keep = res.containers.find((c) => c.name === selectedRef.current) ?? res.containers[0]
        setState((cur) => ({
          ...cur,
          payload: res,
          selectedContainer: keep?.name ?? '',
          drafts: buildDrafts(keep),
          loading: false,
          conflict: false,
          error: null,
          savedNotice: null,
        }))
      })
      .catch((err: unknown) => {
        if (ctrl.signal.aborted) return
        setState((cur) => ({
          ...cur,
          loading: false,
          error: err instanceof Error ? err.message : 'Failed to load workload environment',
        }))
      })
    return () => ctrl.abort()
  }, [workloadId, reloadNonce, selectedRef, setState])
}

async function submitEnvPatch(
  params: {
    workloadId: string
    resourceVersion: string
    targetContainer: string
    drafts: readonly DraftEnvRow[]
  },
  setState: React.Dispatch<React.SetStateAction<EnvEditorState>>,
) {
  setState((cur) => ({ ...cur, saving: true, error: null, savedNotice: null }))
  try {
    const updated = await patchWorkloadEnv({
      id: params.workloadId,
      resourceVersion: params.resourceVersion,
      container: params.targetContainer,
      env: params.drafts.map((d) => ({ name: d.name, value: d.value })),
    })
    const nextCont = updated.containers.find((c) => c.name === params.targetContainer) ?? updated.containers[0]
    setState((cur) => ({
      ...cur,
      payload: updated,
      selectedContainer: nextCont?.name ?? '',
      drafts: buildDrafts(nextCont),
      saving: false,
      conflict: false,
      error: null,
      savedNotice: `Saved · resourceVersion ${updated.resourceVersion}`,
    }))
  } catch (err: unknown) {
    const isConflict = err instanceof WorkloadEnvHttpError && err.status === 409
    const message = err instanceof Error ? err.message : 'Failed to update workload environment'
    setState((cur) => ({ ...cur, saving: false, conflict: isConflict, error: message }))
  }
}

export function useWorkloadEnv(workloadId: string | undefined) {
  const [st, setState] = useState<EnvEditorState>(initialState)
  const [reloadNonce, setReloadNonce] = useState(0)
  const seqRef = useRef(0)
  const selectedRef = useRef('')
  selectedRef.current = st.selectedContainer

  useFetchWorkloadEnv(workloadId, reloadNonce, selectedRef, setState)

  const activeContainer = st.payload?.containers.find((c) => c.name === st.selectedContainer) ?? st.payload?.containers[0]
  const readOnlyVars = extractReadOnly(activeContainer)
  const busy = st.loading || st.saving

  const selectContainer = (name: string) => {
    if (busy) return
    const next = st.payload?.containers.find((c) => c.name === name)
    if (!next) return
    setState((cur) => ({
      ...cur,
      selectedContainer: next.name,
      drafts: buildDrafts(next),
      conflict: false,
      error: null,
      savedNotice: null,
    }))
  }

  const updateDraft = (rowId: string, patch: { name?: string; value?: string }) => {
    if (busy) return
    setState((cur) => ({
      ...cur,
      drafts: cur.drafts.map((r) => (r.rowId === rowId ? { ...r, ...patch } : r)),
      error: null,
      savedNotice: null,
    }))
  }

  const addDraft = () => {
    if (busy) return
    seqRef.current += 1
    const row: DraftEnvRow = { rowId: `new:${seqRef.current}`, name: '', value: '', isNew: true }
    setState((cur) => ({ ...cur, drafts: [...cur.drafts, row], error: null, savedNotice: null }))
  }

  const removeDraft = (rowId: string) => {
    if (busy) return
    setState((cur) => ({ ...cur, drafts: cur.drafts.filter((r) => r.rowId !== rowId), error: null, savedNotice: null }))
  }

  const reset = () => {
    if (busy) return
    setState((cur) => ({ ...cur, drafts: buildDrafts(activeContainer), conflict: false, error: null, savedNotice: null }))
  }

  const save = async () => {
    if (busy || !workloadId || !st.payload || !activeContainer) return
    const vErr = validateDrafts(st.drafts, readOnlyVars)
    if (vErr) {
      setState((cur) => ({ ...cur, error: vErr }))
      return
    }
    await submitEnvPatch(
      { workloadId, resourceVersion: st.payload.resourceVersion, targetContainer: activeContainer.name, drafts: st.drafts },
      setState,
    )
  }

  return {
    containers: st.payload?.containers ?? [],
    activeContainerName: activeContainer?.name ?? '',
    drafts: st.drafts,
    readOnlyVars,
    dirty: isDirty(st.drafts, activeContainer),
    loading: st.loading,
    saving: st.saving,
    busy,
    conflict: st.conflict,
    error: st.error,
    savedNotice: st.savedNotice,
    selectContainer,
    updateDraft,
    addDraft,
    removeDraft,
    reset,
    reload: () => {
      if (!st.saving) setReloadNonce((n) => n + 1)
    },
    save,
  }
}
