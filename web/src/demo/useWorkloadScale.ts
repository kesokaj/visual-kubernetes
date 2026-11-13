import { useEffect, useState } from 'react'
import {
  DemoHttpError,
  fetchWorkloadScale,
  patchWorkloadScale,
  type WorkloadScalePayload,
} from './configApi'

type State = {
  readonly payload: WorkloadScalePayload | null
  readonly draft: string
  readonly loading: boolean
  readonly saving: boolean
  readonly error: string | null
  readonly conflict: boolean
  readonly notice: string | null
}

const initialState: State = {
  payload: null,
  draft: '',
  loading: false,
  saving: false,
  error: null,
  conflict: false,
  notice: null,
}

function parseDraft(draft: string, min: number, max: number): { value: number | null; validationError: string | null } {
  const trimmed = draft.trim()
  if (trimmed === '' || !/^-?\d+$/.test(trimmed)) {
    return { value: null, validationError: `Enter an integer between ${min} and ${max}.` }
  }
  const n = Number(trimmed)
  if (!Number.isInteger(n) || n < min || n > max) {
    return { value: null, validationError: `Replicas must stay between ${min} and ${max}.` }
  }
  return { value: n, validationError: null }
}

export function useWorkloadScale(workloadId: string) {
  const [st, setSt] = useState<State>(initialState)
  const [reloadTick, setReloadTick] = useState(0)

  useEffect(() => {
    const ctrl = new AbortController()
    setSt((prev) => ({ ...prev, loading: true, error: null, conflict: false, notice: null }))
    fetchWorkloadScale(workloadId, ctrl.signal)
      .then((res) => {
        if (ctrl.signal.aborted) return
        setSt({
          payload: res,
          draft: String(res.replicas),
          loading: false,
          saving: false,
          error: null,
          conflict: false,
          notice: null,
        })
      })
      .catch((err: unknown) => {
        if (ctrl.signal.aborted) return
        setSt({
          payload: null,
          draft: '',
          loading: false,
          saving: false,
          error: err instanceof Error ? err.message : 'Failed to load workload scale',
          conflict: false,
          notice: null,
        })
      })
    return () => ctrl.abort()
  }, [workloadId, reloadTick])

  const min = st.payload?.minReplicas ?? 0
  const max = st.payload?.maxReplicas ?? 50
  const parsed = st.payload ? parseDraft(st.draft, min, max) : { value: null, validationError: null }
  const dirty = st.payload !== null && parsed.value !== null && parsed.value !== st.payload.replicas
  const busy = st.loading || st.saving

  const setDraft = (next: string) => {
    setSt((prev) => ({ ...prev, draft: next, error: null, notice: null }))
  }

  const step = (delta: number) => {
    if (!st.payload) return
    const base = parsed.value ?? st.payload.replicas
    const clamped = Math.max(min, Math.min(max, base + delta))
    setDraft(String(clamped))
  }

  const reset = () => {
    if (!st.payload) return
    setSt((prev) => ({
      ...prev,
      draft: String(st.payload?.replicas ?? 0),
      error: null,
      conflict: false,
      notice: null,
    }))
  }

  const reload = () => setReloadTick((n) => n + 1)

  const save = async () => {
    if (!st.payload || busy || !dirty) return
    if (parsed.value === null) {
      setSt((prev) => ({ ...prev, error: parsed.validationError }))
      return
    }
    setSt((prev) => ({ ...prev, saving: true, error: null, conflict: false, notice: null }))
    try {
      const updated = await patchWorkloadScale({
        id: st.payload.id,
        resourceVersion: st.payload.resourceVersion,
        replicas: parsed.value,
      })
      setSt({
        payload: updated,
        draft: String(updated.replicas),
        loading: false,
        saving: false,
        error: null,
        conflict: false,
        notice: `Scaled to ${updated.replicas} ${updated.replicas === 1 ? 'replica' : 'replicas'} (rv ${updated.resourceVersion}).`,
      })
    } catch (err: unknown) {
      const conflict = err instanceof DemoHttpError && err.status === 409
      const error = err instanceof Error ? err.message : 'Failed to update replica count'
      setSt((prev) => ({ ...prev, saving: false, error, conflict }))
    }
  }

  return {
    ...st,
    min,
    max,
    parsedValue: parsed.value,
    validationError: parsed.validationError,
    dirty,
    busy,
    setDraft,
    step,
    reset,
    reload,
    save,
  }
}
