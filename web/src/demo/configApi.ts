// Runtime-validated client helpers and target discovery for Service type, workload scale,
// and autoscaler demo controls (/api/demo/services/type, /workloads/scale, /autoscalers).

import type { Graph } from '../lib/graph'
import type { Entity } from '../lib/wire'

export const ALLOWED_SERVICE_TYPES = ['ClusterIP', 'LoadBalancer'] as const
export type ServiceType = (typeof ALLOWED_SERVICE_TYPES)[number]

export type ServiceTypePayload = {
  readonly id: string
  readonly resourceVersion: string
  readonly type: string
  readonly allowedTypes: readonly ServiceType[]
  readonly clusterIP: string
  readonly headless: boolean
}

export type WorkloadScalePayload = {
  readonly id: string
  readonly kind: 'Deployment' | 'StatefulSet'
  readonly resourceVersion: string
  readonly replicas: number
  readonly minReplicas: number
  readonly maxReplicas: number
  readonly managedByHPA?: string
}

export type HPASpecPayload = {
  readonly minReplicas: number
  readonly maxReplicas: number
  readonly currentReplicas: number
}

export type VPAContainerPolicy = {
  readonly containerName: string
  readonly maxCPU: string
  readonly maxMemory: string
}

export type VPASpecPayload = {
  readonly updateMode: string
  readonly allowedModes: readonly string[]
  readonly containers: readonly VPAContainerPolicy[]
}

export type AutoscalerPayload = {
  readonly id: string
  readonly kind: 'HorizontalPodAutoscaler' | 'VerticalPodAutoscaler'
  readonly resourceVersion: string
  readonly hpa?: HPASpecPayload
  readonly vpa?: VPASpecPayload
}

export type ScalableWorkloadInfo = {
  readonly id: string
  readonly kind: 'Deployment' | 'StatefulSet' | 'DaemonSet'
  readonly readOnlyReason?: string
}

export class DemoHttpError extends Error {
  readonly status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'DemoHttpError'
    this.status = status
  }
}

const WORKLOAD_ID_RE = /^apps\/(Deployment|StatefulSet|DaemonSet)\/([^/]+)\/([^/]+)$/
const AUTOSCALER_KINDS = new Set(['HorizontalPodAutoscaler', 'VerticalPodAutoscaler'])

export function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
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

export function serviceReadOnlyReason(svc: Entity): string | null {
  if (isProtectedNamespace(svc.namespace)) {
    return 'System namespace Services are read-only in demo mode.'
  }
  if (svc.namespace === 'default' && svc.name === 'kubernetes') {
    return 'default/kubernetes API service cannot be modified.'
  }
  return null
}

export function findServicesForEntity(graph: Graph, entity: Entity, role: string): Entity[] {
  if (role === 'sat' && entity.kind === 'Service') return [entity]
  if (role !== 'app') return []
  const linkedIds = new Set<string>()
  for (const edge of graph.edges.values()) {
    if (edge.kind === 'selects' && edge.to === entity.id) {
      linkedIds.add(edge.from)
    }
  }
  const out: Entity[] = []
  for (const candidate of graph.entities.values()) {
    if (candidate.kind !== 'Service' || candidate.folded) continue
    if (candidate.appId === entity.id || linkedIds.has(candidate.id)) {
      out.push(candidate)
    }
  }
  out.sort((a, b) => a.id.localeCompare(b.id))
  return out
}

export function findScalableWorkload(entity: Entity, role: string): ScalableWorkloadInfo | null {
  if (role !== 'app' || !entity.ownerId) return null
  const m = WORKLOAD_ID_RE.exec(entity.ownerId)
  if (!m) return null
  const kind = m[1] as 'Deployment' | 'StatefulSet' | 'DaemonSet'
  const ns = m[2] ?? ''
  if (isProtectedNamespace(entity.namespace) || isProtectedNamespace(ns)) {
    return {
      id: entity.ownerId,
      kind,
      readOnlyReason: 'System namespace workloads are read-only in demo mode.',
    }
  }
  if (kind === 'DaemonSet') {
    return {
      id: entity.ownerId,
      kind,
      readOnlyReason: 'DaemonSet runs 1 pod per matching node (not scaled by replica count).',
    }
  }
  return { id: entity.ownerId, kind }
}

export function findAutoscalersForEntity(graph: Graph, entity: Entity, role: string): Entity[] {
  if (role === 'sat' && AUTOSCALER_KINDS.has(entity.kind)) return [entity]
  if (role !== 'app') return []
  const linkedIds = new Set<string>()
  for (const edge of graph.edges.values()) {
    if ((edge.kind === 'references' || edge.kind === 'routes') && edge.to === entity.id) {
      linkedIds.add(edge.from)
    }
  }
  const out: Entity[] = []
  for (const candidate of graph.entities.values()) {
    if (candidate.folded || !AUTOSCALER_KINDS.has(candidate.kind)) continue
    if (candidate.appId === entity.id || linkedIds.has(candidate.id)) {
      out.push(candidate)
    }
  }
  out.sort((a, b) => a.id.localeCompare(b.id))
  return out
}

export function parseServiceTypePayload(v: unknown): ServiceTypePayload | null {
  if (
    !isRecord(v) ||
    typeof v.id !== 'string' ||
    typeof v.resourceVersion !== 'string' ||
    typeof v.type !== 'string' ||
    !Array.isArray(v.allowedTypes) ||
    typeof v.clusterIP !== 'string' ||
    typeof v.headless !== 'boolean'
  ) {
    return null
  }
  const allowedTypes = v.allowedTypes.filter(
    (t): t is ServiceType => t === 'ClusterIP' || t === 'LoadBalancer',
  )
  return {
    id: v.id,
    resourceVersion: v.resourceVersion,
    type: v.type,
    allowedTypes,
    clusterIP: v.clusterIP,
    headless: v.headless,
  }
}

export function parseWorkloadScalePayload(v: unknown): WorkloadScalePayload | null {
  if (
    !isRecord(v) ||
    typeof v.id !== 'string' ||
    (v.kind !== 'Deployment' && v.kind !== 'StatefulSet') ||
    typeof v.resourceVersion !== 'string' ||
    !Number.isInteger(v.replicas) ||
    !Number.isInteger(v.minReplicas) ||
    !Number.isInteger(v.maxReplicas)
  ) {
    return null
  }
  if (v.managedByHPA !== undefined && typeof v.managedByHPA !== 'string') return null
  return {
    id: v.id,
    kind: v.kind,
    resourceVersion: v.resourceVersion,
    replicas: v.replicas as number,
    minReplicas: v.minReplicas as number,
    maxReplicas: v.maxReplicas as number,
    managedByHPA: typeof v.managedByHPA === 'string' && v.managedByHPA !== '' ? v.managedByHPA : undefined,
  }
}

function parseHPASpec(v: unknown): HPASpecPayload | null {
  if (
    !isRecord(v) ||
    !Number.isInteger(v.minReplicas) ||
    !Number.isInteger(v.maxReplicas) ||
    !Number.isInteger(v.currentReplicas)
  ) {
    return null
  }
  return {
    minReplicas: v.minReplicas as number,
    maxReplicas: v.maxReplicas as number,
    currentReplicas: v.currentReplicas as number,
  }
}

function parseVPAContainer(v: unknown): VPAContainerPolicy | null {
  if (!isRecord(v) || typeof v.containerName !== 'string' || v.containerName === '') return null
  const maxCPU = typeof v.maxCPU === 'string' ? v.maxCPU : ''
  const maxMemory = typeof v.maxMemory === 'string' ? v.maxMemory : ''
  if (v.maxCPU !== undefined && typeof v.maxCPU !== 'string') return null
  if (v.maxMemory !== undefined && typeof v.maxMemory !== 'string') return null
  return { containerName: v.containerName, maxCPU, maxMemory }
}

function parseVPASpec(v: unknown): VPASpecPayload | null {
  if (
    !isRecord(v) ||
    typeof v.updateMode !== 'string' ||
    !Array.isArray(v.allowedModes) ||
    !Array.isArray(v.containers)
  ) {
    return null
  }
  const allowedModes = v.allowedModes.filter((m): m is string => typeof m === 'string' && m !== '')
  const containers: VPAContainerPolicy[] = []
  for (const raw of v.containers) {
    const parsed = parseVPAContainer(raw)
    if (!parsed) return null
    containers.push(parsed)
  }
  return { updateMode: v.updateMode, allowedModes, containers }
}

export function parseAutoscalerPayload(v: unknown): AutoscalerPayload | null {
  if (!isRecord(v) || typeof v.id !== 'string' || typeof v.resourceVersion !== 'string') return null
  if (v.kind === 'HorizontalPodAutoscaler') {
    const hpa = parseHPASpec(v.hpa)
    return hpa ? { id: v.id, kind: v.kind, resourceVersion: v.resourceVersion, hpa } : null
  }
  if (v.kind === 'VerticalPodAutoscaler') {
    const vpa = parseVPASpec(v.vpa)
    return vpa ? { id: v.id, kind: v.kind, resourceVersion: v.resourceVersion, vpa } : null
  }
  return null
}

async function requestConfig<T>(
  url: string,
  init: RequestInit,
  parse: (v: unknown) => T | null,
  malformedMessage: string,
): Promise<T> {
  const resp = await fetch(url, init)
  if (!resp.ok) {
    const text = (await resp.text()).trim()
    throw new DemoHttpError(resp.status, text || `HTTP ${resp.status}`)
  }
  const data: unknown = await resp.json()
  const parsed = parse(data)
  if (!parsed) {
    throw new DemoHttpError(502, malformedMessage)
  }
  return parsed
}

export function fetchServiceType(id: string, signal?: AbortSignal): Promise<ServiceTypePayload> {
  return requestConfig(
    `/api/demo/services/type?id=${encodeURIComponent(id)}`,
    { method: 'GET', headers: { Accept: 'application/json' }, signal },
    parseServiceTypePayload,
    'Malformed service type response',
  )
}

export function patchServiceType(params: {
  readonly id: string
  readonly resourceVersion: string
  readonly type: ServiceType
}): Promise<ServiceTypePayload> {
  return requestConfig(
    '/api/demo/services/type',
    {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify(params),
    },
    parseServiceTypePayload,
    'Malformed service type response',
  )
}

export function fetchWorkloadScale(id: string, signal?: AbortSignal): Promise<WorkloadScalePayload> {
  return requestConfig(
    `/api/demo/workloads/scale?id=${encodeURIComponent(id)}`,
    { method: 'GET', headers: { Accept: 'application/json' }, signal },
    parseWorkloadScalePayload,
    'Malformed workload scale response',
  )
}

export function patchWorkloadScale(params: {
  readonly id: string
  readonly resourceVersion: string
  readonly replicas: number
}): Promise<WorkloadScalePayload> {
  return requestConfig(
    '/api/demo/workloads/scale',
    {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify(params),
    },
    parseWorkloadScalePayload,
    'Malformed workload scale response',
  )
}

export function fetchAutoscaler(id: string, signal?: AbortSignal): Promise<AutoscalerPayload> {
  return requestConfig(
    `/api/demo/autoscalers?id=${encodeURIComponent(id)}`,
    { method: 'GET', headers: { Accept: 'application/json' }, signal },
    parseAutoscalerPayload,
    'Malformed autoscaler response',
  )
}

export function patchAutoscaler(params: {
  readonly id: string
  readonly resourceVersion: string
  readonly hpa?: { readonly minReplicas: number; readonly maxReplicas: number }
  readonly vpa?: {
    readonly updateMode: string
    readonly containers: readonly VPAContainerPolicy[]
  }
}): Promise<AutoscalerPayload> {
  return requestConfig(
    '/api/demo/autoscalers',
    {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify(params),
    },
    parseAutoscalerPayload,
    'Malformed autoscaler response',
  )
}
