import { useEffect, useRef, useState } from 'react'
import {
  DemoHttpError,
  fetchServiceType,
  patchServiceType,
  type ServiceType,
  type ServiceTypePayload,
} from './configApi'

type State = {
  readonly payload: ServiceTypePayload | null
  readonly draftType: ServiceType | null
  readonly loading: boolean
  readonly saving: boolean
  readonly error: string | null
  readonly conflict: boolean
  readonly notice: string | null
}

const initialState: State = {
  payload: null,
  draftType: null,
  loading: false,
  saving: false,
  error: null,
  conflict: false,
  notice: null,
}

function asDraftType(type: string): ServiceType | null {
  return type === 'ClusterIP' || type === 'LoadBalancer' ? type : null
}

export function useServiceType(
  serviceId: string,
  onTypeChanged: (serviceId: string, nextType: string) => void,
) {
  const [st, setSt] = useState<State>(initialState)
  const [reloadTick, setReloadTick] = useState(0)
  const onChangedRef = useRef(onTypeChanged)
  onChangedRef.current = onTypeChanged

  useEffect(() => {
    const ctrl = new AbortController()
    setSt((prev) => ({ ...prev, loading: true, error: null, conflict: false, notice: null }))
    fetchServiceType(serviceId, ctrl.signal)
      .then((res) => {
        if (ctrl.signal.aborted) return
        setSt({
          payload: res,
          draftType: asDraftType(res.type),
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
          draftType: null,
          loading: false,
          saving: false,
          error: err instanceof Error ? err.message : 'Failed to load Service type',
          conflict: false,
          notice: null,
        })
      })
    return () => ctrl.abort()
  }, [serviceId, reloadTick])

  const selectType = (next: ServiceType) => {
    setSt((prev) => ({ ...prev, draftType: next, error: null, notice: null }))
  }

  const reload = () => setReloadTick((n) => n + 1)

  const dirty = st.payload !== null && st.draftType !== null && st.draftType !== st.payload.type
  const busy = st.loading || st.saving

  const save = async () => {
    if (!st.payload || !st.draftType || busy || st.payload.headless || !dirty) return
    setSt((prev) => ({ ...prev, saving: true, error: null, conflict: false, notice: null }))
    try {
      const updated = await patchServiceType({
        id: st.payload.id,
        resourceVersion: st.payload.resourceVersion,
        type: st.draftType,
      })
      setSt({
        payload: updated,
        draftType: asDraftType(updated.type),
        loading: false,
        saving: false,
        error: null,
        conflict: false,
        notice: `Updated to ${updated.type} (rv ${updated.resourceVersion}).`,
      })
      onChangedRef.current(updated.id, updated.type)
    } catch (err: unknown) {
      const conflict = err instanceof DemoHttpError && err.status === 409
      const error = err instanceof Error ? err.message : 'Failed to update Service type'
      setSt((prev) => ({ ...prev, saving: false, error, conflict }))
    }
  }

  return {
    ...st,
    busy,
    dirty,
    selectType,
    reload,
    save,
  }
}
