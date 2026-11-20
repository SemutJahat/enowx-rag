import { useState } from 'react'
import { Loader2, LogIn, Moon, Sun } from 'lucide-react'
import { api, ApiError, errorText, setToken } from '../lib/api'
import { ErrorBox, SecretInput } from './ui'

interface LoginProps {
  theme: 'light' | 'dark'
  onToggleTheme: () => void
  onSuccess: () => void
  /** Why the login is shown (e.g. the stored token was rejected). */
  reason?: string
}

export function Brand() {
  return (
    <div className="brand">
      <div className="brand-mark">
        <img className="brand-img brand-img-light" src="/icon-light-512.png" alt="enowx-rag" />
        <img className="brand-img brand-img-dark" src="/icon-dark-512.png" alt="" aria-hidden="true" />
      </div>
      <div className="brand-name">enowx<span>·rag</span></div>
    </div>
  )
}

/** Admin-token login screen. Validates the token with GET /api/server. */
export function Login({ theme, onToggleTheme, onSuccess, reason }: LoginProps) {
  const [token, setTokenValue] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const submit = async () => {
    const t = token.trim()
    if (!t || busy) return
    setBusy(true)
    setError('')
    try {
      await api.validateToken(t)
      setToken(t)
      onSuccess()
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) setError('That token was rejected by the server.')
      else if (e instanceof ApiError && e.status === 403) setError('This token is not allowed to use the dashboard.')
      else setError(errorText(e, 'Could not reach the server.'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="login-shell">
      <button className="icon-btn login-theme" onClick={onToggleTheme} title="Toggle theme">
        {theme === 'dark' ? <Sun size={15} strokeWidth={1.7} /> : <Moon size={15} strokeWidth={1.7} />}
      </button>
      <form
        className="login-card"
        onSubmit={(e) => {
          e.preventDefault()
          submit()
        }}
      >
        <Brand />
        <h1>Sign in to this server</h1>
        <p className="login-sub">This dashboard is protected by an admin token.</p>
        {reason && <div className="login-reason">{reason}</div>}
        <div className="field">
          <label htmlFor="admin-token">Admin token</label>
          <SecretInput id="admin-token" value={token} onChange={setTokenValue} placeholder="paste your admin token" autoFocus />
        </div>
        {error && <ErrorBox>{error}</ErrorBox>}
        <button className="btn primary login-btn" type="submit" disabled={!token.trim() || busy}>
          {busy ? <Loader2 size={14} className="spin" /> : <LogIn size={14} />}
          Sign in
        </button>
        <div className="login-hint">
          <b>Where is my token?</b>
          <ul>
            <li>Printed by the installer at the end of the server setup.</li>
            <li>The value of <span className="mono">RAG_ADMIN_TOKEN</span> in the server's environment (e.g. its docker-compose or systemd unit).</li>
            <li>Generated earlier under Settings → Admin token, stored in the server's <span className="mono">config.yaml</span>.</li>
          </ul>
          The token is kept in this browser only (localStorage).
        </div>
      </form>
    </div>
  )
}
