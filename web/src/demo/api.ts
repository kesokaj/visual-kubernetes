// Runtime-validated client for /api/demo/* (Phases 1-3, ADR-010, ADR-011).

export type ExecShell = '/bin/sh' | '/bin/bash'

export type ExecSession = {
  readonly podId: string
  readonly namespace: string
  readonly pod: string
  readonly container?: string
  readonly shell: ExecShell
}

export type ExecServerFrame =
  | {
      readonly type: 'ready'
      readonly container: string
      readonly containers: readonly string[]
      readonly shell: ExecShell
    }
  | { readonly type: 'stdout'; readonly data: string }
  | { readonly type: 'exit'; readonly message: string }

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

export type WorkloadEnvItem =
  | { readonly kind: 'plain'; readonly name: string; readonly value: string }
  | { readonly kind: 'readOnly'; readonly name: string; readonly readOnlySource: string }

export type WorkloadContainerEnv = {
  readonly name: string
  readonly env: readonly WorkloadEnvItem[]
}

export type WorkloadEnvPayload = {
  readonly id: string
  readonly resourceVersion: string
  readonly containers: readonly WorkloadContainerEnv[]
}

export class WorkloadEnvHttpError extends Error {
  readonly status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'WorkloadEnvHttpError'
    this.status = status
  }
}

const ENV_NAME_RE = /^[A-Za-z_][A-Za-z0-9_]{0,252}$/
const WORKLOAD_ID_RE = /^apps\/(Deployment|StatefulSet|DaemonSet)\/([^/]+)\/([^/]+)$/

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

export function isValidEnvName(name: string): boolean {
  return ENV_NAME_RE.test(name)
}

export function editableWorkloadId(entity: {
  readonly id: string
  readonly namespace: string
  readonly ownerId?: string
}): string | undefined {
  if (isProtectedNamespace(entity.namespace)) return undefined
  const candidate = entity.id.startsWith('app/') ? entity.ownerId : entity.id
  if (!candidate) return undefined
  const m = WORKLOAD_ID_RE.exec(candidate)
  const ns = m?.[2]
  if (!m || !ns || isProtectedNamespace(ns)) return undefined
  return candidate
}

function parseWorkloadEnvItem(v: unknown): WorkloadEnvItem | null {
  if (!isRecord(v) || typeof v.name !== 'string' || v.name === '') return null
  if (typeof v.readOnlySource === 'string' && v.readOnlySource !== '') {
    return { kind: 'readOnly', name: v.name, readOnlySource: v.readOnlySource }
  }
  if (typeof v.value === 'string') {
    return { kind: 'plain', name: v.name, value: v.value }
  }
  return null
}

function parseWorkloadContainer(v: unknown): WorkloadContainerEnv | null {
  if (!isRecord(v) || typeof v.name !== 'string' || v.name === '' || !Array.isArray(v.env)) return null
  const env: WorkloadEnvItem[] = []
  for (const raw of v.env) {
    const parsed = parseWorkloadEnvItem(raw)
    if (!parsed) return null
    env.push(parsed)
  }
  return { name: v.name, env }
}

export function parseWorkloadEnvPayload(v: unknown): WorkloadEnvPayload | null {
  if (!isRecord(v) || typeof v.id !== 'string' || typeof v.resourceVersion !== 'string' || !Array.isArray(v.containers)) {
    return null
  }
  const containers: WorkloadContainerEnv[] = []
  for (const raw of v.containers) {
    const parsed = parseWorkloadContainer(raw)
    if (!parsed) return null
    containers.push(parsed)
  }
  return { id: v.id, resourceVersion: v.resourceVersion, containers }
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

export async function fetchWorkloadEnv(id: string, signal?: AbortSignal): Promise<WorkloadEnvPayload> {
  const resp = await fetch(`/api/demo/workloads/env?id=${encodeURIComponent(id)}`, {
    method: 'GET',
    headers: { Accept: 'application/json' },
    signal,
  })
  if (!resp.ok) {
    const text = (await resp.text()).trim()
    throw new WorkloadEnvHttpError(resp.status, text || `HTTP ${resp.status}`)
  }
  const data: unknown = await resp.json()
  const parsed = parseWorkloadEnvPayload(data)
  if (!parsed) {
    throw new WorkloadEnvHttpError(502, 'Malformed workload environment response')
  }
  return parsed
}

export async function patchWorkloadEnv(params: {
  id: string
  resourceVersion: string
  container: string
  env: readonly { readonly name: string; readonly value: string }[]
}): Promise<WorkloadEnvPayload> {
  const resp = await fetch('/api/demo/workloads/env', {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
    body: JSON.stringify(params),
  })
  if (!resp.ok) {
    const text = (await resp.text()).trim()
    throw new WorkloadEnvHttpError(resp.status, text || `HTTP ${resp.status}`)
  }
  const data: unknown = await resp.json()
  const parsed = parseWorkloadEnvPayload(data)
  if (!parsed) {
    throw new WorkloadEnvHttpError(502, 'Malformed workload environment response')
  }
  return parsed
}

export function isExecShell(v: unknown): v is ExecShell {
  return v === '/bin/sh' || v === '/bin/bash'
}

export function parseExecServerFrame(raw: string): ExecServerFrame | null {
  let v: unknown
  try {
    v = JSON.parse(raw)
  } catch {
    return null
  }
  if (!isRecord(v) || typeof v.type !== 'string') return null
  if (v.type === 'ready') {
    if (typeof v.container !== 'string' || !Array.isArray(v.containers)) return null
    const containers: string[] = []
    for (const c of v.containers) {
      if (typeof c !== 'string' || c === '') return null
      containers.push(c)
    }
    const shell: ExecShell = isExecShell(v.shell) ? v.shell : '/bin/sh'
    return { type: 'ready', container: v.container, containers, shell }
  }
  if (v.type === 'stdout' && typeof v.data === 'string') {
    return { type: 'stdout', data: v.data }
  }
  if (v.type === 'exit') {
    return { type: 'exit', message: typeof v.message === 'string' ? v.message : 'Shell session ended.' }
  }
  return null
}

export function buildExecWsUrl(loc: Location, session: ExecSession): string {
  const proto = loc.protocol === 'https:' ? 'wss:' : 'ws:'
  const params = new URLSearchParams({ id: session.podId, shell: session.shell })
  if (session.container) {
    params.set('container', session.container)
  }
  return `${proto}//${loc.host}/api/demo/exec?${params.toString()}`
}
