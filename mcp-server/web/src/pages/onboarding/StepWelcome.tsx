import { useState } from 'react'
import { ChevronRight, Loader2, Sparkles } from 'lucide-react'
import { errorText, type SetupApplyRequest } from '../../lib/api'
import { formatNumber } from '../../lib/format'
import { ErrorBox, SuccessBox } from '../../components/ui'
import { draftToRequest, fullyFromEnv, storeFromEnv, type StepProps } from './types'

type Tone = 'good' | 'warn' | 'crit' | 'muted'

function DetectItem({ label, value, tone, detail }: { label: string; value: string; tone: Tone; detail?: string }) {
  const color = tone === 'good' ? 'var(--good)' : tone === 'warn' ? 'var(--warn)' : tone === 'crit' ? 'var(--crit)' : 'var(--text-faint)'
  return (
    <div className="env-item detect-item">
      <span className="status-dot" style={{ background: color }} />
      <div className="detect-main">
        <span className="env-label">{label}</span>
        {detail && <span className="detect-detail">{detail}</span>}
      </div>
      <span className={`env-val mono ${tone === 'good' ? 'ok' : ''}`}>{value}</span>
    </div>
  )
}

export function StepWelcome({ cfg, env, onNext, onApply }: StepProps & { onApply: (req: SetupApplyRequest) => Promise<void> }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const d = env.detect
  const allSet = fullyFromEnv(env)

  const finishFast = async () => {
    setBusy(true)
    setError('')
    try {
      await onApply(draftToRequest(cfg, env))
    } catch (e) {
      setError(errorText(e, 'Could not apply the configuration'))
    } finally {
      setBusy(false)
    }
  }

  const store = d?.store
  const versions = [store?.server_version, store?.extension_version ? `pgvector ${store.extension_version}` : ''].filter(Boolean).join(' · ')

  return (
    <div className="card">
      <div className="card-head">
        <h2>Set up this enowx-rag server</h2>
        <span className="step-badge mono">1 / 6</span>
      </div>
      <div className="card-body">
        <p>
          This server will hold the RAG memory for all your machines: it stores chunks in a database, embeds and reranks them with
          Voyage AI and shows everything on this dashboard. Your laptops connect in <b>client mode</b> afterwards.
        </p>

        <div className="field-label">Detected on this server</div>
        {d ? (
          <div className="env-grid">
            <DetectItem
              label="Database"
              value={store?.ok ? 'reachable' : store?.type ? 'not reachable' : 'not set'}
              tone={store?.ok ? 'good' : store?.type && storeFromEnv(env) ? 'crit' : 'muted'}
              detail={[store?.type, versions, store?.dsn_masked, storeFromEnv(env) ? 'from environment' : ''].filter(Boolean).join(' · ') || store?.message}
            />
            <DetectItem
              label="Voyage API key"
              value={d.voyage_key_from_env ? 'from env' : d.voyage_key_set ? 'saved' : 'missing'}
              tone={d.voyage_key_from_env || d.voyage_key_set ? 'good' : 'warn'}
              detail={d.voyage_key_from_env ? 'RAG_VOYAGE_API_KEY' : d.voyage_key_set ? 'in config.yaml' : 'you will enter it in the Models step'}
            />
            <DetectItem
              label="Admin token"
              value={env.status?.auth_required ? 'required' : 'none'}
              tone={env.status?.auth_required ? 'good' : 'warn'}
              detail={env.status?.auth_required ? 'dashboard and API are protected' : 'you can generate one in the Connect step'}
            />
            {d.sqlite_store && d.sqlite_store.points > 0 ? (
              <DetectItem
                label="Existing SQLite memory"
                value={`${formatNumber(d.sqlite_store.points)} chunks`}
                tone="muted"
                detail={d.sqlite_store.path}
              />
            ) : (
              <DetectItem
                label="Config file"
                value={env.status?.config_file_exists ? 'exists' : 'new'}
                tone="muted"
                detail={env.status?.config_path}
              />
            )}
          </div>
        ) : (
          <div className="panel-empty">Detection is unavailable — the following steps ask for everything.</div>
        )}
        {store && !store.ok && store.message && <div className="field-hint">{store.message}</div>}

        {allSet ? (
          <SuccessBox>
            <b>Everything is configured by the environment.</b> Database and Voyage key are set; finishing applies the
            recommended models ({cfg.voyageModel} · {cfg.voyageDim}, reranker {cfg.rerankerModel || 'off'}) and restarts the server.
          </SuccessBox>
        ) : (
          <p className="welcome-explain">
            Next: pick the database, choose Voyage models (with prices and limits), review a live connection test, then apply —
            the server restarts by itself. Everything detected above is pre-filled.
          </p>
        )}
        {error && <ErrorBox>{error}</ErrorBox>}
      </div>
      <div className="nav-buttons">
        {allSet && (
          <button className="btn ghost" onClick={onNext} disabled={busy}>Customize instead</button>
        )}
        <div className="spacer" />
        {allSet ? (
          <button className="btn primary" onClick={finishFast} disabled={busy}>
            {busy ? <Loader2 size={14} className="spin" /> : <Sparkles size={14} />} Everything is configured — finish
          </button>
        ) : (
          <button className="btn primary" onClick={onNext}>
            Get started <ChevronRight size={14} />
          </button>
        )}
      </div>
    </div>
  )
}
