import { createFileRoute } from '@tanstack/react-router'
import { useEffect, useRef, useState } from 'react'
import { FilterPill, allOn } from '../filter/FilterPill'
import { Sprite } from '../icons/sprite'
import { Graph, connect } from '../lib/graph'
import { parseEvent, type FilterGroup } from '../lib/wire'
import { Panel } from '../panel/Panel'
import { useReplay } from '../replay/useReplay'
import { Web } from '../web/Web'

export const Route = createFileRoute('/')({ component: Page, ssr: false })

// typing reports whether a key goes into a text field. The filter switches are checkboxes and
// do not count: after flipping one, F and V must still work (review phase 3, finding 1).
function typing(t: EventTarget | null): boolean {
  if (t instanceof HTMLTextAreaElement) return true
  if (t instanceof HTMLElement && t.isContentEditable) return true
  return t instanceof HTMLInputElement && t.type !== 'checkbox' && t.type !== 'radio'
}

// Keys are the presenter's actions; there are no buttons for them (plan D7).
type Keys = { fit: () => void; pill: () => void; escape: () => void; replay: () => void; pause: () => void; step: () => void }

// keyAction maps a key to its action: F re-fits the camera, V shows or hides the filter pill,
// R replays the last causal chain, Space pauses or resumes it, → shows its next step.
function keyAction(ev: KeyboardEvent, keys: Keys): (() => void) | undefined {
  if (ev.key === ' ') return keys.pause
  if (ev.key === 'ArrowRight') return keys.step
  return { f: keys.fit, v: keys.pill, r: keys.replay }[ev.key.toLowerCase()]
}

// Esc closes the panel and hides the replay caption. Space is taken from the page (it would
// otherwise flip a focused filter switch or scroll).
function useKeys(keys: Keys) {
  const ref = useRef(keys)
  ref.current = keys
  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      if (ev.metaKey || ev.ctrlKey || ev.altKey) return
      if (ev.key === 'Escape') return ref.current.escape()
      if (typing(ev.target)) return
      const act = keyAction(ev, ref.current)
      if (!act) return
      if (ev.key === ' ') ev.preventDefault()
      act()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])
}

function Page() {
  const [graph] = useState(() => new Graph())
  const [on, setOn] = useState<ReadonlySet<FilterGroup>>(allOn)
  const [fitNonce, setFitNonce] = useState(0)
  const [pill, setPill] = useState(true)
  const [selected, setSelected] = useState<string | null>(null)
  const { player, play } = useReplay(graph)

  useEffect(() => connect(graph, parseEvent), [graph])
  const [fitAgain] = useState(() => () => setFitNonce((n) => n + 1))
  const [close] = useState(() => () => setSelected(null))
  useKeys({
    fit: fitAgain,
    pill: () => setPill((p) => !p),
    escape: () => {
      close()
      player.clear()
    },
    replay: () => player.replay(),
    pause: () => player.togglePause(),
    step: () => player.step(),
  })

  const changeFilter = (next: ReadonlySet<FilterGroup>) => {
    setOn(next)
    fitAgain() // hidden groups leave the layout and the web re-fits
  }

  return (
    <main className="vk">
      <Sprite />
      <Web graph={graph} on={on} fitNonce={fitNonce} selected={selected} onSelect={setSelected} play={play} />
      {pill && <FilterPill on={on} onChange={changeFilter} />}
      {selected && <Panel key={selected} graph={graph} id={selected} onClose={close} onSelect={setSelected} />}
    </main>
  )
}
