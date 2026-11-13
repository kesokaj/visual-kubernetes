import { useEffect, useRef, useState } from 'react'
import { fetchDemoPorts, toggleDemoPort, type BrowseSession, type DemoPort } from './api'

type UseDemoPortsArgs = {
  enabled: boolean
  entityId: string
  refreshNonce?: number
  onBrowse: (session: BrowseSession) => void
  onPortClosed: (serviceId: string, port: number) => void
}

function openPortMap(list: readonly DemoPort[]): Map<string, DemoPort> {
  const next = new Map<string, DemoPort>()
  for (const p of list) {
    if (p.open) next.set(`${p.id}:${p.port}`, p)
  }
  return next
}

export function useDemoPorts({ enabled, entityId, refreshNonce = 0, onBrowse, onPortClosed }: UseDemoPortsArgs) {
  const [ports, setPorts] = useState<DemoPort[]>([])
  const [busyKey, setBusyKey] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const openPortsRef = useRef<Map<string, DemoPort>>(new Map())
  const onPortClosedRef = useRef(onPortClosed)
  onPortClosedRef.current = onPortClosed

  useEffect(() => {
    if (!enabled) return
    const ctrl = new AbortController()
    setError(null)
    fetchDemoPorts(ctrl.signal)
      .then((list) => {
        if (ctrl.signal.aborted) return
        const nextOpen = openPortMap(list)
        for (const [key, prev] of openPortsRef.current) {
          if (!nextOpen.has(key)) {
            onPortClosedRef.current(prev.id, prev.port)
          }
        }
        openPortsRef.current = nextOpen
        setPorts(list)
      })
      .catch((err: unknown) => {
        if (!ctrl.signal.aborted) {
          setError(err instanceof Error ? err.message : 'Failed to load demo ports')
        }
      })
    return () => ctrl.abort()
  }, [enabled, entityId, refreshNonce])

  const setOpen = async (p: DemoPort, nextOpen: boolean) => {
    const key = `${p.id}:${p.port}`
    setBusyKey(key)
    setError(null)
    try {
      const updated = await toggleDemoPort({
        namespace: p.namespace,
        service: p.service,
        port: p.port,
        open: nextOpen,
      })
      openPortsRef.current = openPortMap(updated)
      setPorts(updated)
      const fresh = updated.find((it) => it.id === p.id && it.port === p.port)
      if (nextOpen && fresh?.open && fresh.pathPrefix) {
        onBrowse({
          id: fresh.id,
          namespace: fresh.namespace,
          service: fresh.service,
          port: fresh.port,
          pathPrefix: fresh.pathPrefix,
        })
      } else if (!nextOpen) {
        onPortClosedRef.current(p.id, p.port)
      }
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to toggle port')
    } finally {
      setBusyKey(null)
    }
  }

  return { ports, busyKey, error, setOpen }
}
