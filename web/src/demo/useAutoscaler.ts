import { useEffect, useState } from 'react'
import {
  DemoHttpError,
  fetchAutoscaler,
  patchAutoscaler,
  type AutoscalerPayload,
  type HPASpecPayload,
  type VPAContainerPolicy,
  type VPASpecPayload,
} from './configApi'

export type HPADraft = { readonly min: string; readonly max: string }
export type VPADraft = {
  readonly updateMode: string
  readonly containers: readonly VPAContainerPolicy[]
}

type State = {
  readonly payload: AutoscalerPayload | null
  readonly hpaDraft: HPADraft | null
  readonly vpaDraft: VPADraft | null
  readonly loading: boolean
  readonly saving: boolean
  readonly error: string | null
  readonly conflict: boolean
  readonly notFound: boolean
  readonly notice: string | null
}

const initialState: State = {
  payload: null,
  hpaDraft: null,
  vpaDraft: null,
  loading: false,
  saving: false,
  error: null,
  conflict: false,
  notFound: false,
  notice: null,
}

function hpaDraftFrom(spec: HPASpecPayload | undefined): HPADraft | null {
  return spec ? { min: String(spec.minReplicas), max: String(spec.maxReplicas) } : null
}

function vpaDraftFrom(spec: VPASpecPayload | undefined): VPADraft | null {
  if (!spec) return null
  return {
    updateMode: spec.updateMode,
    containers: spec.containers.map((c) => ({ ...c })),
  }
}

function parseHPA(d: HPADraft): { min: number; max: number } | string {
  const minRaw = d.min.trim()
  const maxRaw = d.max.trim()
  if (!/^\d+$/.test(minRaw) || !/^\d+$/.test(maxRaw)) {
    return 'Min and Max replicas must be whole numbers.'
  }
  const min = Number(minRaw)
  const max = Number(maxRaw)
  if (min < 1 || min > 50 || max < 1 || max > 50) {
    return 'HPA replicas must stay between 1 and 50.'
  }
  if (min > max) {
    return 'Min replicas cannot exceed Max replicas.'
  }
  return { min, max }
}

function isVPADirty(d: VPADraft, base: VPASpecPayload): boolean {
  if (d.updateMode !== base.updateMode || d.containers.length !== base.containers.length) return true
  return d.containers.some((c, i) => {
    const b = base.containers[i]
    return !b || c.maxCPU.trim() !== b.maxCPU.trim() || c.maxMemory.trim() !== b.maxMemory.trim()
  })
}

export function useAutoscaler(autoscalerId: string) {
  const [st, setSt] = useState<State>(initialState)
  const [reloadTick, setReloadTick] = useState(0)

  useEffect(() => {
    const ctrl = new AbortController()
    setSt((prev) => ({
      ...prev,
      loading: true,
      error: null,
      conflict: false,
      notFound: false,
      notice: null,
    }))
    fetchAutoscaler(autoscalerId, ctrl.signal)
      .then((res) => {
        if (ctrl.signal.aborted) return
        setSt({
          payload: res,
          hpaDraft: hpaDraftFrom(res.hpa),
          vpaDraft: vpaDraftFrom(res.vpa),
          loading: false,
          saving: false,
          error: null,
          conflict: false,
          notFound: false,
          notice: null,
        })
      })
      .catch((err: unknown) => {
        if (ctrl.signal.aborted) return
        const notFound = err instanceof DemoHttpError && err.status === 404
        setSt({
          payload: null,
          hpaDraft: null,
          vpaDraft: null,
          loading: false,
          saving: false,
          error: err instanceof Error ? err.message : 'Failed to load autoscaler',
          conflict: false,
          notFound,
          notice: null,
        })
      })
    return () => ctrl.abort()
  }, [autoscalerId, reloadTick])

  const parsedHpa = st.payload?.hpa && st.hpaDraft ? parseHPA(st.hpaDraft) : null
  const hpaErr = typeof parsedHpa === 'string' ? parsedHpa : null
  const hpaDirty =
    st.payload?.hpa !== undefined &&
    typeof parsedHpa === 'object' &&
    parsedHpa !== null &&
    (parsedHpa.min !== st.payload.hpa.minReplicas || parsedHpa.max !== st.payload.hpa.maxReplicas)
  const vpaDirty = st.payload?.vpa !== undefined && st.vpaDraft !== null && isVPADirty(st.vpaDraft, st.payload.vpa)
  const dirty = hpaDirty || vpaDirty
  const busy = st.loading || st.saving

  const setHpaField = (field: 'min' | 'max', value: string) => {
    setSt((prev) =>
      prev.hpaDraft
        ? { ...prev, hpaDraft: { ...prev.hpaDraft, [field]: value }, error: null, notice: null }
        : prev,
    )
  }

  const setVpaMode = (updateMode: string) => {
    setSt((prev) =>
      prev.vpaDraft ? { ...prev, vpaDraft: { ...prev.vpaDraft, updateMode }, error: null, notice: null } : prev,
    )
  }

  const setVpaContainer = (containerName: string, patch: { maxCPU?: string; maxMemory?: string }) => {
    setSt((prev) => {
      if (!prev.vpaDraft) return prev
      const containers = prev.vpaDraft.containers.map((c) =>
        c.containerName === containerName ? { ...c, ...patch } : c,
      )
      return { ...prev, vpaDraft: { ...prev.vpaDraft, containers }, error: null, notice: null }
    })
  }

  const reset = () => {
    setSt((prev) => ({
      ...prev,
      hpaDraft: hpaDraftFrom(prev.payload?.hpa),
      vpaDraft: vpaDraftFrom(prev.payload?.vpa),
      error: null,
      conflict: false,
      notice: null,
    }))
  }

  const reload = () => setReloadTick((n) => n + 1)

  const save = async () => {
    if (!st.payload || busy || !dirty) return
    if (st.payload.kind === 'HorizontalPodAutoscaler') {
      if (typeof parsedHpa === 'string' || !parsedHpa) {
        setSt((prev) => ({ ...prev, error: typeof parsedHpa === 'string' ? parsedHpa : 'Invalid HPA bounds' }))
        return
      }
    }
    setSt((prev) => ({ ...prev, saving: true, error: null, conflict: false, notice: null }))
    try {
      const body =
        st.payload.kind === 'HorizontalPodAutoscaler' && typeof parsedHpa === 'object' && parsedHpa
          ? {
              id: st.payload.id,
              resourceVersion: st.payload.resourceVersion,
              hpa: { minReplicas: parsedHpa.min, maxReplicas: parsedHpa.max },
            }
          : {
              id: st.payload.id,
              resourceVersion: st.payload.resourceVersion,
              vpa: {
                updateMode: st.vpaDraft?.updateMode ?? 'Auto',
                containers: (st.vpaDraft?.containers ?? []).map((c) => ({
                  containerName: c.containerName,
                  maxCPU: c.maxCPU.trim(),
                  maxMemory: c.maxMemory.trim(),
                })),
              },
            }
      const updated = await patchAutoscaler(body)
      setSt({
        payload: updated,
        hpaDraft: hpaDraftFrom(updated.hpa),
        vpaDraft: vpaDraftFrom(updated.vpa),
        loading: false,
        saving: false,
        error: null,
        conflict: false,
        notFound: false,
        notice: `Updated ${updated.kind === 'HorizontalPodAutoscaler' ? 'HPA' : 'VPA'} (rv ${updated.resourceVersion}).`,
      })
    } catch (err: unknown) {
      const conflict = err instanceof DemoHttpError && err.status === 409
      const error = err instanceof Error ? err.message : 'Failed to update autoscaler'
      setSt((prev) => ({ ...prev, saving: false, error, conflict }))
    }
  }

  return {
    ...st,
    busy,
    dirty,
    validationError: hpaErr,
    setHpaField,
    setVpaMode,
    setVpaContainer,
    reset,
    reload,
    save,
  }
}
