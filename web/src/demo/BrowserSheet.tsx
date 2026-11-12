import { useState } from 'react'
import type { BrowseSession } from './api'

type Props = {
  session: BrowseSession
  onClose: () => void
}

export function BrowserSheet({ session, onClose }: Props) {
  const [reloadNonce, setReloadNonce] = useState(0)
  const label = `${session.namespace}/${session.service}:${session.port}`

  return (
    <section className="demo-browser" data-testid="demo-browser" aria-label={`Demo proxy ${label}`}>
      <header className="demo-browser-head">
        <div className="demo-browser-title">
          <b>{label}</b>
          <span>Sandboxed demo proxy (non-production)</span>
        </div>
        <div className="demo-browser-controls">
          <button
            type="button"
            className="demo-btn"
            data-action="reload-browser"
            onClick={() => setReloadNonce((n) => n + 1)}
          >
            Reload
          </button>
          <button
            type="button"
            className="p-close"
            aria-label="Close browser view"
            data-action="hide-browser"
            onClick={onClose}
          >
            ×
          </button>
        </div>
      </header>
      <iframe
        key={`${session.pathPrefix}:${reloadNonce}`}
        className="demo-iframe"
        data-testid="demo-iframe"
        title={`Demo view ${label}`}
        src={`${session.pathPrefix}/`}
        sandbox="allow-scripts allow-forms"
        referrerPolicy="no-referrer"
      />
    </section>
  )
}
