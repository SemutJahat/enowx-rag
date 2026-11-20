import { useEffect, useRef, useState } from 'react'
import { CheckCircle2, Loader2, RotateCcw, XCircle } from 'lucide-react'
import { waitForRestart } from '../lib/server'
import type { Health } from '../lib/api'

interface RestartOverlayProps {
  /** boot_id before the restart; the overlay closes once /api/health reports a different one. */
  prevBootId: string
  title?: string
  detail?: string
  onDone: (health: Health) => void
  onDismiss?: () => void
}

/** Full-screen "restarting…" overlay that waits for a new boot_id. */
export function RestartOverlay({ prevBootId, title = 'Restarting the server', detail, onDone, onDismiss }: RestartOverlayProps) {
  const [elapsed, setElapsed] = useState(0)
  const [failed, setFailed] = useState(false)
  const [done, setDone] = useState(false)
  const [attempt, setAttempt] = useState(0)
  const onDoneRef = useRef(onDone)
  onDoneRef.current = onDone

  useEffect(() => {
    const ctrl = new AbortController()
    setFailed(false)
    waitForRestart(prevBootId, { timeoutMs: 45000, signal: ctrl.signal, onTick: setElapsed })
      .then((h) => {
        setDone(true)
        window.setTimeout(() => onDoneRef.current(h), 600)
      })
      .catch(() => {
        if (!ctrl.signal.aborted) setFailed(true)
      })
    return () => ctrl.abort()
  }, [prevBootId, attempt])

  return (
    <div className="overlay" role="alertdialog" aria-live="polite">
      <div className="overlay-card">
        {done ? (
          <CheckCircle2 size={30} className="good-icon" />
        ) : failed ? (
          <XCircle size={30} className="crit-icon" />
        ) : (
          <Loader2 size={30} className="spin accent-icon" />
        )}
        <div className="overlay-title">
          {done ? 'Server is back' : failed ? 'Still waiting for the server' : title}
        </div>
        <div className="overlay-sub">
          {done
            ? 'Reloading data…'
            : failed
              ? 'It has not answered with a new boot id after 45 s. It may still be starting (pulling models, migrating the store), or the new settings failed to load — check the server logs.'
              : detail || 'Saving settings and waiting for the new process to answer /api/health.'}
        </div>
        {!done && !failed && <div className="overlay-meta mono">{Math.round(elapsed / 1000)} s</div>}
        {failed && (
          <div className="overlay-actions">
            <button className="btn" onClick={() => setAttempt((a) => a + 1)}>
              <RotateCcw size={14} /> Keep waiting
            </button>
            <button className="btn" onClick={() => window.location.reload()}>Reload page</button>
            {onDismiss && <button className="btn ghost" onClick={onDismiss}>Close</button>}
          </div>
        )}
      </div>
    </div>
  )
}
