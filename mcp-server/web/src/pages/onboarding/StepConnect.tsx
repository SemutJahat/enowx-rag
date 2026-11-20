import { useState } from 'react'
import { ChevronRight, Loader2, ShieldCheck } from 'lucide-react'
import { api, errorText, getToken, setToken } from '../../lib/api'
import { ClientModeExplainer, ConnectCommands } from '../../components/ConnectCommands'
import { CopyBlock } from '../../components/CopyBlock'
import { ErrorBox, SuccessBox, WarnBox } from '../../components/ui'
import type { ApplyOutcome } from './Wizard'
import type { StepProps } from './types'

export function StepConnect({ env, onNext, outcome }: StepProps & { outcome: ApplyOutcome | null }) {
  const url = window.location.origin
  const [token, setTok] = useState(getToken())
  const [newToken, setNewToken] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const authRequired = !!env.status?.auth_required
  const tokenLocked = (env.status?.env_locked || []).includes('admin_token')

  const generate = async () => {
    setBusy(true)
    setError('')
    try {
      const r = await api.genToken()
      // The server ignores the header until a token is active, so it is safe to use it right away.
      setToken(r.token)
      setTok(r.token)
      setNewToken(r.token)
    } catch (e) {
      setError(errorText(e, 'Could not generate a token'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="card">
      <div className="card-head">
        <h2>Connect your machines</h2>
        <span className="step-badge mono">5 / 6</span>
      </div>
      <div className="card-body">
        {outcome?.restarted && <SuccessBox>Configuration applied — the server restarted with the new settings.</SuccessBox>}
        {outcome?.manualRestart && (
          <WarnBox title="Configuration saved.">Restart the server process or container so it loads the new settings.</WarnBox>
        )}

        <p style={{ marginTop: 14 }}>
          Run these on each laptop or workstation. Your agent then talks to the local <span className="mono">enowx-rag</span>, which
          scans files on that machine and uses this server for embedding, storage and reranking.
        </p>
        <ClientModeExplainer />

        {!token && !authRequired && !tokenLocked && (
          <div className="token-cta">
            <ShieldCheck size={18} />
            <div>
              <b>Protect this server with an admin token.</b> Recommended before exposing it on the internet; machines and the
              dashboard then need the token.
            </div>
            <button className="btn primary" onClick={generate} disabled={busy}>
              {busy ? <Loader2 size={14} className="spin" /> : <ShieldCheck size={14} />} Generate token
            </button>
          </div>
        )}
        {error && <ErrorBox>{error}</ErrorBox>}
        {newToken && (
          <>
            <CopyBlock title="admin token — save it in your password manager" code={newToken} secret={newToken} />
            <div className="field-hint">This browser is signed in with it. It may take effect after the next server restart.</div>
          </>
        )}
        {!token && authRequired && (
          <WarnBox title="The server requires an admin token.">
            Use the token from the installer output or <span className="mono">RAG_ADMIN_TOKEN</span> in place of <span className="mono">&lt;TOKEN&gt;</span>.
          </WarnBox>
        )}

        <ConnectCommands
          url={url}
          token={token || (authRequired ? '<TOKEN>' : '')}
          full={false}
          tokenWarning={authRequired || tokenLocked}
        />
        <div className="field-hint">
          The dashboard's Connect page has these commands again, plus a manual MCP config and <span className="mono">enowx-rag push</span> for existing local memory.
        </div>
      </div>
      <div className="nav-buttons">
        <div className="spacer" />
        <button className="btn primary" onClick={onNext}>
          Next <ChevronRight size={14} />
        </button>
      </div>
    </div>
  )
}
