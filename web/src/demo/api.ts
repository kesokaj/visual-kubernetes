// Runtime-validated client for /api/demo/ports (Phase 1, ADR-010, ADR-011).

export type DemoPort = {
  id: string
  namespace: string
  service: string
  appId: string
  port: number
  name: string
  exposure: string
  open: boolean
  pathPrefix: string
}

export type BrowseSession = {
  id: string
  namespace: string
  service: string
  port: number
  pathPrefix: string
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

export function isDemoPort(v: unknown): v is DemoPort {
  if (!isRecord(v)) return false
  return (
    typeof v.id === 'string' &&
    typeof v.namespace === 'string' &&
    typeof v.service === 'string' &&
    typeof v.appId === 'string' &&
    typeof v.port === 'number' &&
    Number.isInteger(v.port) &&
    v.port >= 1 &&
    v.port <= 65535 &&
    typeof v.name === 'string' &&
    typeof v.exposure === 'string' &&
    typeof v.open === 'boolean' &&
    typeof v.pathPrefix === 'string'
  )
}

export function parsePortsPayload(v: unknown): DemoPort[] {
  if (!isRecord(v) || !Array.isArray(v.ports)) return []
  return v.ports.filter(isDemoPort)
}

export function isProtectedNamespace(ns: string): boolean {
  const s = ns.trim().toLowerCase()
  if (
    s === '' ||
    s === 'kube-system' ||
    s === 'kube-public' ||
    s === 'kube-node-lease' ||
    s === 'visual-kubernetes'
  ) {
    return true
  }
  return s.startsWith('gke-managed-') || s.startsWith('gmp-')
}

export async function fetchDemoPorts(signal?: AbortSignal): Promise<DemoPort[]> {
  const resp = await fetch('/api/demo/ports', {
    method: 'GET',
    headers: { Accept: 'application/json' },
    signal,
  })
  if (!resp.ok) {
    const text = (await resp.text()).trim()
    throw new Error(text || `HTTP ${resp.status}`)
  }
  const data: unknown = await resp.json()
  return parsePortsPayload(data)
}

export async function toggleDemoPort(params: {
  namespace: string
  service: string
  port: number
  open: boolean
}): Promise<DemoPort[]> {
  const resp = await fetch('/api/demo/ports', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
    body: JSON.stringify(params),
  })
  if (!resp.ok) {
    const text = (await resp.text()).trim()
    throw new Error(text || `HTTP ${resp.status}`)
  }
  const data: unknown = await resp.json()
  return parsePortsPayload(data)
}
