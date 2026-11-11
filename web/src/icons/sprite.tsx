import { useMemo } from 'react'

// The vendored Kubernetes icons (README.md, LICENSE), bundled as text and turned into one
// hidden <symbol> sprite so every drawing is a cheap <use href="#k-…">.
const raw = import.meta.glob<string>('./*.svg', { query: '?raw', import: 'default', eager: true })

export type IconName =
  | 'api' | 'c-m' | 'cm' | 'control-plane' | 'cronjob' | 'deploy' | 'ds' | 'ep' | 'etcd'
  | 'hpa' | 'ing' | 'job' | 'netpol' | 'node' | 'pod' | 'pvc' | 'rs' | 'sched' | 'sts' | 'svc'

// Every icon has this viewBox upstream; <use> sites size against it.
export const ICON_VIEWBOX = '0 0 18.035334 17.500378'

const byKind: Record<string, IconName> = {
  Deployment: 'deploy',
  StatefulSet: 'sts',
  DaemonSet: 'ds',
  ReplicaSet: 'rs',
  Job: 'job',
  CronJob: 'cronjob',
  Pod: 'pod',
  Node: 'node',
  APIServer: 'api',
  Etcd: 'etcd',
  Scheduler: 'sched',
  ControllerManager: 'c-m',
  Service: 'svc',
  ConfigMap: 'cm',
  HorizontalPodAutoscaler: 'hpa',
  VerticalPodAutoscaler: 'hpa',
  Ingress: 'ing',
  Gateway: 'ing',
  HTTPRoute: 'ing',
  NetworkPolicy: 'netpol',
  PersistentVolumeClaim: 'pvc',
}

// iconFor picks the icon for a kind; unknown workload kinds draw as a Deployment, anything
// else as the generic control-plane box.
export function iconFor(kind: string, isApp: boolean): IconName {
  return byKind[kind] ?? (isApp ? 'deploy' : 'control-plane')
}

// Inkscape metadata and the labels baked into the three control-plane icons are dropped: the
// page draws its own labels at a readable size (PRD R10). Anything that could run code is
// dropped too, as defence in depth: the files are vendored and pinned by
// TestVendoredIconsCarryNoActiveContent, and the CSP blocks inline script that is not hashed.
const dropped = new Set(['metadata', 'namedview', 'defs', 'text', 'title', 'script', 'foreignobject'])

// stripActive matches tag names case-insensitively (XML keeps `<Script>` distinct, the HTML
// serialization would not) and drops every href outright: the icons are shapes and link nothing,
// so no scheme check is needed (security review phase 3, round 2).
function stripActive(root: Element) {
  for (const el of Array.from(root.getElementsByTagName('*'))) {
    if (dropped.has(el.localName.toLowerCase())) el.remove()
  }
  for (const el of [root, ...Array.from(root.getElementsByTagName('*'))]) {
    for (const a of Array.from(el.attributes)) {
      if (/^on/i.test(a.localName) || a.localName.toLowerCase() === 'href') el.removeAttribute(a.name)
    }
  }
}

function symbolMarkup(file: string, src: string): string {
  const name = file.replace(/^\.\//, '').replace(/\.svg$/, '')
  const doc = new DOMParser().parseFromString(src, 'image/svg+xml')
  const root = doc.documentElement
  stripActive(root)
  const ser = new XMLSerializer()
  const inner = Array.from(root.children, (c) => ser.serializeToString(c)).join('')
  const viewBox = root.getAttribute('viewBox') ?? ICON_VIEWBOX
  return `<symbol id="k-${name}" viewBox="${viewBox}">${inner}</symbol>`
}

// Sprite renders the hidden symbol sheet once. Its markup comes only from the bundled icon
// files above, never from the stream.
export function Sprite() {
  const html = useMemo(
    () => Object.entries(raw).map(([file, src]) => symbolMarkup(file, src)).join(''),
    [],
  )
  return (
    <svg
      width="0"
      height="0"
      style={{ position: 'absolute' }}
      aria-hidden="true"
      data-testid="icon-sprite"
      dangerouslySetInnerHTML={{ __html: html }}
    />
  )
}

export function Icon({ name, size, x, y }: { name: IconName; size: number; x: number; y: number }) {
  return <use href={`#k-${name}`} x={x - size / 2} y={y - size / 2} width={size} height={size} />
}

// Globe is the internet (ext/internet): drawn inline after design/mockup-v3.html globe(), since
// the vendored Kubernetes set has no globe (plan D5). Centred on (x, y), size wide.
export function Globe({ size, x, y }: { size: number; x: number; y: number }) {
  const r = size / 2
  const lat = `M${x - r} ${y}H${x + r}M${x - r * 0.85} ${y - r * 0.5}H${x + r * 0.85}M${x - r * 0.85} ${y + r * 0.5}H${x + r * 0.85}`
  return (
    <g className="globe" data-icon="globe">
      <circle cx={x} cy={y} r={r} fill="#fff" stroke="#326ce5" strokeWidth={2} />
      <ellipse cx={x} cy={y} rx={r * 0.45} ry={r} fill="none" stroke="#326ce5" strokeWidth={1.5} />
      <path d={lat} stroke="#326ce5" strokeWidth={1.2} fill="none" />
    </g>
  )
}
