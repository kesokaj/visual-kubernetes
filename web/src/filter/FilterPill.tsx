import { useState } from 'react'
import { ICON_VIEWBOX, type IconName } from '../icons/sprite'
import { FILTER_GROUPS, type FilterGroup } from '../lib/wire'

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

export const allOn = (): ReadonlySet<FilterGroup> => new Set(FILTER_GROUPS)

type Props = {
  on: ReadonlySet<FilterGroup>
  onChange: (next: ReadonlySet<FilterGroup>) => void
}

// FilterPill is the only chrome on the page: a small pill bottom-left that opens a list of
// switches, all on by default (plan D3). The parent hides it on V.
export function FilterPill({ on, onChange }: Props) {
  const [open, setOpen] = useState(false)
  const shown = GROUPS.filter((g) => on.has(g.id)).length
  const toggle = (id: FilterGroup) => {
    const next = new Set(on)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    onChange(next)
  }
  return (
    <div className="filter" data-testid="filter" onPointerDown={(e) => e.stopPropagation()}>
      {open && (
        <div className="f-list" role="group" aria-label="Show component groups">
          {GROUPS.map((g) => (
            <label key={g.id} className={on.has(g.id) ? 'f-row' : 'f-row off'} data-group={g.id}>
              <svg viewBox={ICON_VIEWBOX} aria-hidden="true">
                <use href={`#k-${g.icon}`} />
              </svg>
              <span>{g.label}</span>
              <input
                type="checkbox"
                role="switch"
                className="f-sw"
                checked={on.has(g.id)}
                onChange={() => toggle(g.id)}
                aria-label={g.label}
              />
            </label>
          ))}
          <div className="f-foot">
            <button type="button" className="f-all" onClick={() => onChange(allOn())}>
              All on
            </button>
            <span>V hides this</span>
          </div>
        </div>
      )}
      <button type="button" className="f-pill" aria-expanded={open} onClick={() => setOpen(!open)}>
        <svg viewBox="0 0 24 24" aria-hidden="true">
          <path d="M3 5h18l-7 8v6l-4-2v-4z" />
        </svg>
        <span>{shown === GROUPS.length ? 'Showing all' : `${shown} of ${GROUPS.length} shown`}</span>
      </button>
    </div>
  )
}
