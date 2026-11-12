import type { ExecSession, ExecShell } from './api'
import { type ExecStatus, usePodExec } from './usePodExec'

type Props = {
  readonly session: ExecSession
  readonly onClose: () => void
}

function statusBadgeLabel(status: ExecStatus): string {
  switch (status) {
    case 'connecting':
      return 'Connecting'
    case 'connected':
      return 'Connected'
    case 'exited':
      return 'Exited'
    case 'closed':
      return 'Disconnected'
  }
}

const SHELLS: readonly ExecShell[] = ['/bin/sh', '/bin/bash']

export function ExecDrawer({ session, onClose }: Props) {
  const {
    termHostRef,
    status,
    container,
    containers,
    shell,
    exitMessage,
    selectContainer,
    selectShell,
    reconnect,
  } = usePodExec(session)

  const podLabel = `${session.namespace}/${session.pod}`

  return (
    <section
      className="demo-exec-drawer"
      data-testid="exec-drawer"
      data-exec-pod={session.podId}
      data-exec-status={status}
      aria-label={`Interactive terminal for ${podLabel}`}
    >
      <header className="demo-browser-head">
        <div className="demo-browser-title">
          <b>{podLabel}</b>
          <span>
            Interactive pod shell{container ? ` · ${container}` : ''} · non-production demo
          </span>
        </div>
        <div className="demo-browser-controls">
          <span className={`demo-badge ${status === 'connected' ? 'open' : 'closed'}`} data-testid="exec-status">
            {statusBadgeLabel(status)}
          </span>
          {containers.length > 1 && (
            <div className="chips" role="tablist" aria-label="Pod containers">
              {containers.map((c) => (
                <button
                  type="button"
                  role="tab"
                  key={c}
                  aria-selected={c === container}
                  className={`chip${c === container ? ' active' : ''}`}
                  data-testid={`exec-container-${c}`}
                  onClick={() => selectContainer(c)}
                >
                  {c}
                </button>
              ))}
            </div>
          )}
          <div className="chips" role="group" aria-label="Shell executable">
            {SHELLS.map((sh) => (
              <button
                type="button"
                key={sh}
                className={`chip${sh === shell ? ' active' : ''}`}
                data-testid={sh === '/bin/sh' ? 'exec-shell-sh' : 'exec-shell-bash'}
                onClick={() => selectShell(sh)}
              >
                {sh}
              </button>
            ))}
          </div>
          {(status === 'exited' || status === 'closed') && (
            <button type="button" className="demo-btn primary" data-testid="exec-reconnect" onClick={reconnect}>
              Reconnect
            </button>
          )}
          <button
            type="button"
            className="demo-btn"
            data-testid="exec-close"
            aria-label="Close terminal"
            onClick={onClose}
          >
            Close
          </button>
        </div>
      </header>
      {exitMessage && (
        <div className="demo-exec-banner" role="status" data-testid="exec-exit">
          {exitMessage}
        </div>
      )}
      <div ref={termHostRef} className="demo-exec-term" data-testid="exec-term" />
    </section>
  )
}
