import type { Entity } from '../lib/wire'

// Counters are the corner tallies for what the web does not draw one by one: the model's
// counter/* entities (unreferenced ConfigMaps, undrawn kinds, S5 folding) and the page's
// over-budget counter. They are HTML, outside the zoomed world, so they keep 12px+ text (R10).
export function Counters({ counters }: { counters: readonly Entity[] }) {
  if (counters.length === 0) return null
  return (
    <aside className="counters" data-testid="counters" aria-label="Folded and uncounted objects">
      {counters.map((c) => (
        <div key={c.id} className="counter-chip" data-counter={c.id} data-count={c.count ?? 0}>
          {c.status || `${c.count ?? 0} ${c.name}`}
        </div>
      ))}
    </aside>
  )
}
