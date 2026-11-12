import type { Terminal } from '@xterm/xterm'
import { useCallback, useEffect, useRef, useState } from 'react'
import { buildExecWsUrl, type ExecSession, type ExecShell, parseExecServerFrame } from './api'

export type ExecStatus = 'connecting' | 'connected' | 'exited' | 'closed'

const TERM_FONT_PX = 13
const TERM_COLS = 88
const TERM_ROWS = 20

type ExecConnCallbacks = {
  readonly onReady: (container: string, containers: readonly string[]) => void
  readonly onExit: (message: string) => void
  readonly onUnexpectedClose: () => void
}

function sendJson(ws: WebSocket, payload: Record<string, unknown>) {
  if (ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify(payload))
  }
}

function connectPodExecSocket(wsUrl: string, term: Terminal, cb: ExecConnCallbacks): () => void {
  let exited = false
  let disposed = false
  const ws = new WebSocket(wsUrl)

  ws.onopen = () => {
    if (disposed) return
    sendJson(ws, { type: 'resize', cols: term.cols, rows: term.rows })
    term.focus()
  }

  ws.onmessage = (ev: MessageEvent) => {
    if (disposed || typeof ev.data !== 'string') return
    const frame = parseExecServerFrame(ev.data)
    if (!frame) return
    if (frame.type === 'ready') {
      cb.onReady(frame.container, frame.containers)
      return
    }
    if (frame.type === 'stdout') {
      term.write(frame.data)
      return
    }
    exited = true
    term.writeln(`\r\n[${frame.message}]`)
    cb.onExit(frame.message)
  }

  ws.onclose = () => {
    if (!disposed && !exited) cb.onUnexpectedClose()
  }

  const dataSub = term.onData((data) => sendJson(ws, { type: 'stdin', data }))
  const resizeSub = term.onResize(({ cols, rows }) => sendJson(ws, { type: 'resize', cols, rows }))

  return () => {
    disposed = true
    dataSub.dispose()
    resizeSub.dispose()
    if (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING) {
      ws.close()
    }
  }
}

export function usePodExec(initial: ExecSession) {
  const termHostRef = useRef<HTMLDivElement | null>(null)
  const [requestedContainer, setRequestedContainer] = useState<string>(initial.container ?? '')
  const [container, setContainer] = useState<string>(initial.container ?? '')
  const [containers, setContainers] = useState<readonly string[]>([])
  const [shell, setShell] = useState<ExecShell>(initial.shell)
  const [status, setStatus] = useState<ExecStatus>('connecting')
  const [exitMessage, setExitMessage] = useState<string | null>(null)
  const [nonce, setNonce] = useState(0)

  useEffect(() => {
    setRequestedContainer(initial.container ?? '')
    setContainer(initial.container ?? '')
    setContainers([])
    setShell(initial.shell)
    setExitMessage(null)
    setStatus('connecting')
  }, [initial.podId, initial.container, initial.shell])

  useEffect(() => {
    const host = termHostRef.current
    if (!host) return
    let cancelled = false
    let cleanupConn: (() => void) | undefined
    let term: Terminal | undefined

    setStatus('connecting')
    setExitMessage(null)

    // Promise rejections are caught below to transition status out of 'connecting' and expose Reconnect.
    void import('@xterm/xterm')
      .then(({ Terminal: XTerm }) => {
        if (cancelled || !termHostRef.current) return
        termHostRef.current.replaceChildren()
        term = new XTerm({
          cols: TERM_COLS,
          rows: TERM_ROWS,
          fontSize: TERM_FONT_PX,
          lineHeight: 1.35,
          fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
          cursorBlink: true,
          screenReaderMode: true,
          theme: {
            background: '#0f172a',
            foreground: '#f8fafc',
            cursor: '#60a5fa',
          },
        })
        term.open(termHostRef.current)
        term.attachCustomKeyEventHandler((ev) => ev.key !== 'Escape')

        const wsUrl = buildExecWsUrl(window.location, {
          podId: initial.podId,
          namespace: initial.namespace,
          pod: initial.pod,
          container: requestedContainer || undefined,
          shell,
        })

        cleanupConn = connectPodExecSocket(wsUrl, term, {
          onReady: (resolvedContainer, allContainers) => {
            setContainer(resolvedContainer)
            setContainers(allContainers)
            setStatus('connected')
            setExitMessage(null)
          },
          onExit: (msg) => {
            setStatus('exited')
            setExitMessage(msg)
          },
          onUnexpectedClose: () => {
            setStatus('closed')
            setExitMessage('Terminal connection closed (pod unavailable, forbidden, or session limit reached).')
          },
        })
      })
      .catch((err: unknown) => {
        if (cancelled) return
        setStatus('closed')
        setExitMessage(err instanceof Error ? err.message : 'Failed to load terminal bundle.')
      })

    return () => {
      cancelled = true
      cleanupConn?.()
      term?.dispose()
    }
  }, [initial.podId, initial.namespace, initial.pod, requestedContainer, shell, nonce])

  const selectContainer = useCallback((next: string) => {
    setRequestedContainer(next)
    setContainer(next)
  }, [])

  const selectShell = useCallback((next: ExecShell) => {
    setShell(next)
  }, [])

  const reconnect = useCallback(() => {
    setNonce((n) => n + 1)
  }, [])

  return {
    termHostRef,
    status,
    container,
    containers,
    shell,
    exitMessage,
    selectContainer,
    selectShell,
    reconnect,
  }
}
