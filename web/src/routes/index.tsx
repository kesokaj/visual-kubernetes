import { createFileRoute } from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import { FilterPill, allOn } from '../filter/FilterPill'
import { Sprite } from '../icons/sprite'
import { Graph, connect } from '../lib/graph'
import { parseEvent, type FilterGroup } from '../lib/wire'
import { Web } from '../web/Web'

export const Route = createFileRoute('/')({ component: Page, ssr: false })

// typing reports whether a key goes into a text field. The filter switches are checkboxes and
// do not count: after flipping one, F and V must still work (review phase 3, finding 1).
function typing(t: EventTarget | null): boolean {
  if (t instanceof HTMLTextAreaElement) return true
  if (t instanceof HTMLElement && t.isContentEditable) return true
  return t instanceof HTMLInputElement && t.type !== 'checkbox' && t.type !== 'radio'
}

// Presenter keys this phase: F re-fits the camera, V shows or hides the filter pill.
function useKeys(onFit: () => void, onTogglePill: () => void) {
  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      if (ev.metaKey || ev.ctrlKey || ev.altKey) return
      if (typing(ev.target)) return
      const k = ev.key.toLowerCase()
      if (k === 'f') onFit()
      else if (k === 'v') onTogglePill()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onFit, onTogglePill])
}

function Page() {
  const [graph] = useState(() => new Graph())
  const [on, setOn] = useState<ReadonlySet<FilterGroup>>(allOn)
  const [fitNonce, setFitNonce] = useState(0)
  const [pill, setPill] = useState(true)

  useEffect(() => connect(graph, parseEvent), [graph])
  const [fitAgain] = useState(() => () => setFitNonce((n) => n + 1))
  const [togglePill] = useState(() => () => setPill((p) => !p))
  useKeys(fitAgain, togglePill)

  const changeFilter = (next: ReadonlySet<FilterGroup>) => {
    setOn(next)
    fitAgain() // hidden groups leave the layout and the web re-fits
  }

  return (
    <main className="vk">
      <Sprite />
      <Web graph={graph} on={on} fitNonce={fitNonce} />
      {pill && <FilterPill on={on} onChange={changeFilter} />}
    </main>
  )
}
