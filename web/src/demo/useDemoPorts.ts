import { useEffect, useState } from 'react'
import { fetchDemoPorts, toggleDemoPort, type BrowseSession, type DemoPort } from './api'

type UseDemoPortsArgs = {
  enabled: boolean
  entityId: string
  onBrowse: (session: BrowseSession) => void
  onPortClosed: (serviceId: string, port: number) => void
}

export function useDemoPorts({ enabled, entityId, onBrowse, onPortClosed }: UseDemoPortsArgs) {
  const [ports, setPorts] = useState<DemoPort[]>([])
  const [busyKey, setBusyKey] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!enabled) return
    const ctrl = new AbortController()
    setError(null)
    fetchDemoPorts(ctrl.signal)
      .then((list) => setPorts(list))
      .catch((err: unknown) => {
        if (!ctrl.signal.aborted) {
          setError(err instanceof Error ? err.message : 'Failed to load demo ports')
        }
      })
    return () => ctrl.abort()
  }, [enabled, entityId])

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
        onPortClosed(p.id, p.port)
      }
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to toggle port')
    } finally {
      setBusyKey(null)
    }
  }

  return { ports, busyKey, error, setOpen }
}
