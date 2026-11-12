import { createFileRoute } from '@tanstack/react-router'
import { useEffect, useRef, useState } from 'react'
import type { BrowseSession, ExecSession } from '../demo/api'
import { BrowserSheet } from '../demo/BrowserSheet'
import { ExecDrawer } from '../demo/ExecDrawer'
import { FilterPill } from '../filter/FilterPill'
import { defaultFilter, type FilterState } from '../filter/state'
import { Sprite } from '../icons/sprite'
import { Graph, connect } from '../lib/graph'
import { parseEvent } from '../lib/wire'
import { Panel } from '../panel/Panel'
import { useReplay } from '../replay/useReplay'
import type { WebControls } from '../web/useWeb'
import { Web } from '../web/Web'

export const Route = createFileRoute('/')({ component: Page, ssr: false })

// typing reports whether a key goes into a text field. The filter switches are checkboxes and
// do not count: after flipping one, F and V must still work (review phase 3, finding 1).
function typing(t: EventTarget | null): boolean {
  if (t instanceof HTMLTextAreaElement) return true
  if (t instanceof HTMLElement && t.isContentEditable) return true
  return t instanceof HTMLInputElement && t.type !== 'checkbox' && t.type !== 'radio'
}

// Keys are the presenter's actions; only fit and zoom also have buttons, in the filter pill.
type Keys = {
  fit: () => void
  pill: () => void
  escape: () => void
  replay: () => void
  pause: () => void
  step: () => void
  zoomIn: () => void
  zoomOut: () => void
}

// keyAction maps a key to its action: F re-fits the camera, V shows or hides the filter pill,
// R replays the last causal chain, Space pauses or resumes it, → shows its next step, + and −
// zoom around the screen centre.
function keyAction(ev: KeyboardEvent, keys: Keys): (() => void) | undefined {
  if (ev.key === ' ') return keys.pause
  if (ev.key === 'ArrowRight') return keys.step
  const map: Record<string, () => void> = {
    f: keys.fit,
    v: keys.pill,
    r: keys.replay,
    '+': keys.zoomIn,
    '=': keys.zoomIn,
    '-': keys.zoomOut,
    _: keys.zoomOut,
  }
  return map[ev.key.toLowerCase()]
}

const ZOOM_STEP = 1.4 // one zoom button press or + / − key

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

function refitDue(prev: FilterState, next: FilterState): boolean {
  if (prev.groups !== next.groups || prev.showSelf !== next.showSelf || prev.mode !== next.mode) {
    return true
  }
  return next.mode === 'only' && (prev.kinds !== next.kinds || prev.namespaces !== next.namespaces)
}

function Page() {
  const [graph] = useState(() => new Graph())
  const [filter, setFilter] = useState<FilterState>(defaultFilter)
  const [fitNonce, setFitNonce] = useState(0)
  const [pill, setPill] = useState(true)
  const [selected, setSelected] = useState<string | null>(null)
  const [browse, setBrowse] = useState<BrowseSession | null>(null)
  const [execSession, setExecSession] = useState<ExecSession | null>(null)
  const { player, play } = useReplay(graph)

  useEffect(() => connect(graph, parseEvent), [graph])
  const [fitAgain] = useState(() => () => setFitNonce((n) => n + 1))
  const [close] = useState(() => () => setSelected(null))
  const handlePortClosed = (serviceId: string, port: number) => {
    setBrowse((cur) => (cur && cur.id === serviceId && cur.port === port ? null : cur))
  }
  const controls = useRef<WebControls | null>(null)
  const [zoom] = useState(() => ({
    in: () => controls.current?.zoomBy(ZOOM_STEP),
    out: () => controls.current?.zoomBy(1 / ZOOM_STEP),
    fit: fitAgain,
  }))
  useKeys({
    fit: fitAgain,
    pill: () => setPill((p) => !p),
    escape: () => {
      setExecSession(null)
      setBrowse(null)
      close()
      player.clear()
    },
    replay: () => player.replay(),
    pause: () => player.togglePause(),
    step: () => player.step(),
    zoomIn: zoom.in,
    zoomOut: zoom.out,
  })

  const changeFilter = (next: FilterState) => {
    if (refitDue(filter, next)) fitAgain()
    setFilter(next)
  }

  return (
    <main className="vk">
      <Sprite />
      <Web
        graph={graph}
        filter={filter}
        fitNonce={fitNonce}
        selected={selected}
        onSelect={setSelected}
        play={play}
        controlsRef={controls}
      />
      {pill && <FilterPill graph={graph} filter={filter} onChange={changeFilter} zoom={zoom} />}
      {browse && <BrowserSheet session={browse} onClose={() => setBrowse(null)} />}
      {execSession && <ExecDrawer session={execSession} onClose={() => setExecSession(null)} />}
      {selected && (
        <Panel
          key={selected}
          graph={graph}
          id={selected}
          onClose={close}
          onSelect={setSelected}
          onBrowse={setBrowse}
          onPortClosed={handlePortClosed}
          onOpenExec={setExecSession}
        />
      )}
    </main>
  )
}
