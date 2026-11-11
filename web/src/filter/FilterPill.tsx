import { useMemo, useState, useSyncExternalStore } from 'react'
import { ICON_VIEWBOX, type IconName } from '../icons/sprite'
import type { Graph } from '../lib/graph'
import type { FilterGroup } from '../lib/wire'
import {
  availableNamespaces,
  defaultFilter,
  pillLabel,
  toggleInSet,
  type FilterState,
  type WorkloadKind,
} from './state'

// The eight groups in mockup order (design/mockup-v3.html FILTERS), keyed by the model's
// filterGroup values (internal/model/model.go).
export const GROUPS: readonly { id: FilterGroup; label: string; icon: IconName }[] = [
  { id: 'controlPlane', label: 'Control plane', icon: 'api' },
  { id: 'nodes', label: 'Nodes', icon: 'node' },
  { id: 'workloads', label: 'Workloads', icon: 'deploy' },
  { id: 'pods', label: 'Pods', icon: 'pod' },
  { id: 'servicesConfig', label: 'Services & config', icon: 'svc' },
  { id: 'traffic', label: 'Traffic', icon: 'ep' },
  { id: 'ingress', label: 'Internet & ingress', icon: 'ing' },
  { id: 'system', label: 'System add-ons', icon: 'ds' },
]

const KINDS: readonly { kind: WorkloadKind; icon: IconName }[] = [
  { kind: 'Deployment', icon: 'deploy' },
  { kind: 'StatefulSet', icon: 'sts' },
  { kind: 'DaemonSet', icon: 'ds' },
]

type Props = {
  graph: Graph
  filter: FilterState
  onChange: (next: FilterState) => void
  zoom?: ZoomButtons
}

// ZoomButtons are the presenter's camera actions; the same ones answer +, − and F.
export type ZoomButtons = { in: () => void; out: () => void; fit: () => void }

// ZoomRow is the end of the pill itself (one capsule, R6/D2): zoom out, fit everything (auto-follow back on), zoom in.
function ZoomRow({ zoom }: { zoom: ZoomButtons }) {
  return (
    <div className="f-zoom" role="group" aria-label="Zoom">
      <button type="button" aria-label="Zoom out" title="Zoom out (−)" onClick={zoom.out}>
        −
      </button>
      <button type="button" aria-label="Fit" title="Fit everything (F)" onClick={zoom.fit}>
        Fit
      </button>
      <button type="button" aria-label="Zoom in" title="Zoom in (+)" onClick={zoom.in}>
        +
      </button>
    </div>
  )
}

function SwitchRow({
  icon,
  label,
  ariaLabel,
  checked,
  group,
  onToggle,
}: {
  icon: IconName
  label: string
  ariaLabel?: string
  checked: boolean
  group?: string
  onToggle: () => void
}) {
  return (
    <label className={checked ? 'f-row' : 'f-row off'} data-group={group}>
      <svg viewBox={ICON_VIEWBOX} aria-hidden="true">
        <use href={`#k-${icon}`} />
      </svg>
      <span>{label}</span>
      <input
        type="checkbox"
        role="switch"
        className="f-sw"
        checked={checked}
        onChange={onToggle}
        aria-label={ariaLabel ?? label}
      />
    </label>
  )
}

function ModeToggle({
  mode,
  onSelect,
}: {
  mode: FilterState['mode']
  onSelect: (mode: FilterState['mode']) => void
}) {
  return (
    <div className="f-mode" role="group" aria-label="Filter mode">
      <button
        type="button"
        className={mode === 'highlight' ? 'f-mode-btn on' : 'f-mode-btn'}
        aria-pressed={mode === 'highlight'}
        onClick={() => onSelect('highlight')}
      >
        Highlight
      </button>
      <button
        type="button"
        className={mode === 'only' ? 'f-mode-btn on' : 'f-mode-btn'}
        aria-pressed={mode === 'only'}
        onClick={() => onSelect('only')}
      >
        Only show
      </button>
    </div>
  )
}

function NamespaceChips({
  namespaces,
  selected,
  onToggle,
}: {
  namespaces: readonly string[]
  selected: ReadonlySet<string>
  onToggle: (ns: string) => void
}) {
  if (namespaces.length === 0) return null
  return (
    <>
      <div className="f-sub">Namespaces</div>
      <div className="f-chips f-ns" role="group" aria-label="Namespaces">
        {namespaces.map((ns) => {
          const active = selected.has(ns)
          return (
            <button
              key={ns}
              type="button"
              className={active ? 'f-chip on' : 'f-chip'}
              aria-pressed={active}
              onClick={() => onToggle(ns)}
            >
              <span>{ns}</span>
            </button>
          )
        })}
      </div>
    </>
  )
}

function FocusControls({
  filter,
  namespaces,
  onChange,
}: {
  filter: FilterState
  namespaces: readonly string[]
  onChange: (next: FilterState) => void
}) {
  return (
    <div className="f-sec">
      <div className="f-sec-head">
        <span className="f-sec-title">Focus</span>
        <ModeToggle mode={filter.mode} onSelect={(mode) => onChange({ ...filter, mode })} />
      </div>
      <div className="f-chips" role="group" aria-label="Workload kinds">
        {KINDS.map(({ kind, icon }) => {
          const active = filter.kinds.has(kind)
          return (
            <button
              key={kind}
              type="button"
              className={active ? 'f-chip on' : 'f-chip'}
              aria-pressed={active}
              onClick={() => onChange({ ...filter, kinds: toggleInSet(filter.kinds, kind) })}
            >
              <svg viewBox={ICON_VIEWBOX} aria-hidden="true">
                <use href={`#k-${icon}`} />
              </svg>
              <span>{kind}</span>
            </button>
          )
        })}
      </div>
      <NamespaceChips
        namespaces={namespaces}
        selected={filter.namespaces}
        onToggle={(ns) => onChange({ ...filter, namespaces: toggleInSet(filter.namespaces, ns) })}
      />
    </div>
  )
}

// FilterPill is the only chrome on the page: a compact pill bottom-left that opens component
// switches (all on by default), self-visibility (off by default), and workload/namespace focus,
// with the zoom buttons beside it.
export function FilterPill({ graph, filter, onChange, zoom }: Props) {
  const [open, setOpen] = useState(false)
  const version = useSyncExternalStore(graph.subscribe, graph.getVersion, graph.getVersion)
  const namespaces = useMemo(
    () => availableNamespaces(graph.entities, filter.showSelf, filter.namespaces),
    [graph, version, filter.showSelf, filter.namespaces],
  )
  return (
    <div className="filter" data-testid="filter" onPointerDown={(e) => e.stopPropagation()}>
      {open && (
        <div className="f-list">
          <div role="group" aria-label="Show component groups">
            {GROUPS.map((g) => (
              <SwitchRow
                key={g.id}
                icon={g.icon}
                label={g.label}
                checked={filter.groups.has(g.id)}
                group={g.id}
                onToggle={() => onChange({ ...filter, groups: toggleInSet(filter.groups, g.id) })}
              />
            ))}
          </div>
          <div className="f-sep" />
          <SwitchRow
            icon="deploy"
            label="visual-kubernetes"
            ariaLabel="Show visual-kubernetes"
            checked={filter.showSelf}
            group="self"
            onToggle={() => onChange({ ...filter, showSelf: !filter.showSelf })}
          />
          <div className="f-sep" />
          <FocusControls filter={filter} namespaces={namespaces} onChange={onChange} />
          <div className="f-foot">
            <button type="button" className="f-all" onClick={() => onChange(defaultFilter())}>
              All on
            </button>
            <span>V hides this</span>
          </div>
        </div>
      )}
      <div className="f-bar">
        <button type="button" className="f-pill" aria-expanded={open} onClick={() => setOpen(!open)}>
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <path d="M3 5h18l-7 8v6l-4-2v-4z" />
          </svg>
          <span>{pillLabel(filter, GROUPS.length)}</span>
        </button>
        {zoom && <ZoomRow zoom={zoom} />}
      </div>
    </div>
  )
}
