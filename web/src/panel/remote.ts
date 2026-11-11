import { useEffect, useState } from 'react'

// MAX_TEXT caps what the panel keeps of a response: a huge object or log tail must not stall
// the page during a demo.
const MAX_TEXT = 256 * 1024

export type Remote = { state: 'idle' | 'loading' | 'ok' | 'error'; status?: number; text: string }

// useText fetches a same-origin text endpoint (/api/objects, /api/logs) and re-fetches when
// url or nonce changes; a newer request aborts the older one. The body is only ever shown as
// text, never parsed as HTML.
export function useText(url: string | null, nonce = 0): Remote {
  const [res, setRes] = useState<Remote>({ state: url ? 'loading' : 'idle', text: '' })
  useEffect(() => {
    if (!url) {
      setRes({ state: 'idle', text: '' })
      return
    }
    const ctl = new AbortController()
    setRes({ state: 'loading', text: '' })
    fetch(url, { signal: ctl.signal, credentials: 'same-origin', headers: { accept: 'text/plain' } })
      .then(async (r) => {
        const body = await r.text()
        const text = body.length > MAX_TEXT ? `${body.slice(0, MAX_TEXT)}\n… (truncated)` : body
        setRes(r.ok ? { state: 'ok', status: r.status, text } : { state: 'error', status: r.status, text: `${r.status} ${text.trim()}` })
      })
      .catch((err: unknown) => {
        if (!ctl.signal.aborted) setRes({ state: 'error', text: err instanceof Error ? err.message : String(err) })
      })
    return () => ctl.abort()
  }, [url, nonce])
  return res
}

export function objectUrl(id: string | undefined): string | null {
  return id ? `/api/objects?${new URLSearchParams({ id })}` : null
}

export function logsUrl(podId: string | undefined): string | null {
  return podId ? `/api/logs?${new URLSearchParams({ id: podId, tailLines: '200' })}` : null
}
