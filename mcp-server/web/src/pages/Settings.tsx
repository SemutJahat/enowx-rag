import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { Save, RefreshCw, Zap, Loader2, Power, Wand2, KeyRound, Cpu, Database, Timer, Server as ServerIcon, ShieldCheck, CheckCircle2 } from 'lucide-react'
import {
  api, errorText, setPendingToken, setToken,
  type ConfigMasked, type ConfigUpdate, type ConfigUpdateResponse, type ModelTestResponse, type ModelsResponse, type ServerInfo, type SetupTestResponse,
} from '../lib/api'
import { useServerStatus } from '../lib/server'
import { formatBytes, formatDuration, formatMs, formatNumber, fullTime } from '../lib/format'
import { EmbedModelPicker, ModelTestResult, ReembedImpact, RerankPicker, reembedImpact } from '../components/ModelPicker'
import { RestartOverlay } from '../components/RestartOverlay'
import { CopyBlock } from '../components/CopyBlock'
import { ConfirmDialog, EnvLock, ErrorBox, SecretInput, Spinner, TestResultRow, WarnBox } from '../components/ui'

interface SettingsProps {
  onRunWizard: () => void
}

type SaveFn = (patch: ConfigUpdate) => Promise<ConfigUpdateResponse>

const SECTIONS: { id: string; label: string; icon: typeof Cpu }[] = [
  { id: 'models', label: 'Models', icon: Cpu },
  { id: 'keys', label: 'API keys', icon: KeyRound },
  { id: 'token', label: 'Admin token', icon: ShieldCheck },
  { id: 'autosync', label: 'Auto-sync', icon: Timer },
  { id: 'database', label: 'Database', icon: Database },
  { id: 'server', label: 'Server', icon: ServerIcon },
]

export function Settings({ onRunWizard }: SettingsProps) {
  const [server, setServer] = useState<ServerInfo | null>(null)
  const [config, setConfig] = useState<ConfigMasked | null>(null)
  const [models, setModels] = useState<ModelsResponse | null>(null)
  const [loadError, setLoadError] = useState('')
  const [restart, setRestart] = useState<{ prevBootId: string; detail?: string } | null>(null)
  const { markRestarting, refresh: refreshHealth } = useServerStatus()

  const reload = useCallback(() => {
    setLoadError('')
    Promise.all([api.server(), api.getConfig()])
      .then(([s, c]) => {
        setServer(s)
        setConfig(c)
      })
      .catch((e) => setLoadError(errorText(e, 'Failed to load settings')))
    api.models().then(setModels).catch(() => setModels({ embedding: [], rerank: [], compat_note: '', source: '' }))
  }, [])

  useEffect(reload, [reload])

  const envLocked = useMemo(() => new Set([...(config?.env_locked || []), ...(server?.env_locked || [])]), [config, server])

  const save: SaveFn = useCallback(async (patch) => {
    const res = await api.updateConfig(patch)
    if (res.restarting && server) {
      markRestarting()
      setRestart({ prevBootId: server.boot_id, detail: 'Saving settings and waiting for the server to start with them.' })
    } else {
      reload()
    }
    return res
  }, [server, reload, markRestarting])

  const restartNow = useCallback(async () => {
    if (!server) return
    await api.restartServer()
    markRestarting()
    setRestart({ prevBootId: server.boot_id })
  }, [server, markRestarting])

  const scrollTo = (id: string) => document.getElementById(`settings-${id}`)?.scrollIntoView({ behavior: 'smooth', block: 'start' })

  return (
    <>
      <div className="page-head">
        <h1>Settings</h1>
        <span className="id mono">{server?.config_path || ''}</span>
      </div>

      <div className="section-nav">
        {SECTIONS.map((s) => {
          const Icon = s.icon
          return (
            <button key={s.id} type="button" className="chip-btn" onClick={() => scrollTo(s.id)}>
              <Icon size={13} /> {s.label}
            </button>
          )
        })}
      </div>

      {loadError && <ErrorBox>{loadError}</ErrorBox>}
      {!server || !config ? (
        !loadError && <Spinner label="Loading settings…" />
      ) : (
        <>
          <ModelsSection server={server} config={config} models={models} envLocked={envLocked} save={save} restartNow={restartNow} />
          <KeysSection server={server} config={config} envLocked={envLocked} save={save} restartNow={restartNow} />
          <TokenSection config={config} envLocked={envLocked} onChanged={reload} />
          <AutoSyncSection server={server} config={config} envLocked={envLocked} save={save} restartNow={restartNow} />
          <DatabaseSection server={server} config={config} envLocked={envLocked} save={save} restartNow={restartNow} />
          <ServerSection server={server} restartNow={restartNow} onRunWizard={onRunWizard} />
        </>
      )}

      {restart && (
        <RestartOverlay
          prevBootId={restart.prevBootId}
          detail={restart.detail}
          onDone={() => {
            setRestart(null)
            refreshHealth()
            reload()
          }}
          onDismiss={() => setRestart(null)}
        />
      )}
    </>
  )
}

// ---------------------------------------------------------------------------

function Section({ id, title, hint, children }: { id: string; title: string; hint?: ReactNode; children: ReactNode }) {
  return (
    <section className="panel settings-section" id={`settings-${id}`}>
      <div className="panel-head">
        <h2>{title}</h2>
        {hint && <span className="hint">{hint}</span>}
      </div>
      <div className="panel-body">{children}</div>
    </section>
  )
}

/** Outcome of a save: saved / restart needed (+ button) / warnings / error. */
function SaveResult({ res, error, restartNow }: { res: ConfigUpdateResponse | null; error: string; restartNow: () => Promise<void> }) {
  const [busy, setBusy] = useState(false)
  const [restartErr, setRestartErr] = useState('')
  if (error) return <ErrorBox>{error}</ErrorBox>
  if (!res) return null
  return (
    <div className="save-result">
      {res.restarting ? null : res.restart_required ? (
        <WarnBox title="Saved — restart required.">
          The new value is in the config file but the running server still uses the old one.{' '}
          <button
            className="btn small"
            disabled={busy}
            onClick={async () => {
              setBusy(true)
              setRestartErr('')
              try {
                await restartNow()
              } catch (e) {
                setRestartErr(errorText(e, 'Restart failed'))
              } finally {
                setBusy(false)
              }
            }}
          >
            {busy ? <Loader2 size={13} className="spin" /> : <Power size={13} />} Restart now
          </button>
          {restartErr && <div className="crit-text">{restartErr}</div>}
        </WarnBox>
      ) : (
        <div className="saved-line"><CheckCircle2 size={14} /> Saved.</div>
      )}
      {res.warnings?.length > 0 && (
        <WarnBox title="Warnings:">
          <ul className="plain-list">{res.warnings.map((w, i) => <li key={i}>{w}</li>)}</ul>
        </WarnBox>
      )}
    </div>
  )
}

function useSave(save: SaveFn) {
  const [busy, setBusy] = useState(false)
  const [res, setRes] = useState<ConfigUpdateResponse | null>(null)
  const [error, setError] = useState('')
  const run = useCallback(async (patch: ConfigUpdate) => {
    setBusy(true)
    setError('')
    setRes(null)
    try {
      const r = await save(patch)
      setRes(r)
      return r
    } catch (e) {
      setError(errorText(e, 'Save failed'))
      return null
    } finally {
      setBusy(false)
    }
  }, [save])
  return { busy, res, error, run }
}

interface SectionProps {
  server: ServerInfo
  config: ConfigMasked
  envLocked: Set<string>
  save: SaveFn
  restartNow: () => Promise<void>
}

// ---------------------------------------------------------------------------
// Models
// ---------------------------------------------------------------------------

function ModelsSection({ server, config, models, envLocked, save, restartNow }: SectionProps & { models: ModelsResponse | null }) {
  const current = { model: server.embedder.model, dim: server.embedder.dim }
  const currentRerank = server.reranker.enabled ? server.reranker.model : ''
  const [model, setModel] = useState(current.model)
  const [dim, setDim] = useState(current.dim)
  const [rerank, setRerank] = useState(currentRerank)
  const [testing, setTesting] = useState(false)
  const [test, setTest] = useState<ModelTestResponse | null>(null)
  const [testErr, setTestErr] = useState('')
  const saver = useSave(save)

  // Follow the server after a restart.
  const bootRef = useRef(server.boot_id)
  useEffect(() => {
    if (bootRef.current === server.boot_id) return
    bootRef.current = server.boot_id
    setModel(server.embedder.model)
    setDim(server.embedder.dim)
    setRerank(server.reranker.enabled ? server.reranker.model : '')
  }, [server])

  const isVoyage = server.embedder.type === 'voyage'
  const lockedModel = envLocked.has('voyage_model')
  const lockedDim = envLocked.has('voyage_dim')
  const lockedRerank = envLocked.has('reranker_model')
  const changedEmbed = isVoyage && (model !== current.model || dim !== current.dim)
  const changedRerank = rerank !== currentRerank
  const dirty = changedEmbed || changedRerank
  const impact = models ? reembedImpact(models.embedding, current, { model, dim }) : 'none'
  const rows = server.store.rows ?? 0

  const runTest = async () => {
    setTesting(true)
    setTestErr('')
    setTest(null)
    try {
      setTest(await api.testModels({ embed_model: isVoyage ? model : current.model, dim: isVoyage ? dim : undefined, rerank_model: rerank || undefined }))
    } catch (e) {
      setTestErr(errorText(e, 'Test failed'))
    } finally {
      setTesting(false)
    }
  }

  const apply = () => {
    const patch: ConfigUpdate = { restart: server.restart_supported }
    if (isVoyage && model !== current.model && !lockedModel) patch.voyage_model = model
    if (isVoyage && dim !== current.dim && !lockedDim) patch.voyage_dim = dim
    if (changedRerank && !lockedRerank) patch.reranker_model = rerank
    saver.run(patch)
  }

  const reset = () => {
    setModel(current.model)
    setDim(current.dim)
    setRerank(currentRerank)
    setTest(null)
  }

  return (
    <Section id="models" title="Models (Voyage AI)" hint={models?.source ? `catalog: ${models.source}` : undefined}>
      {!models ? (
        <Spinner label="Loading model catalog…" />
      ) : (
        <>
          <div className="sub-head first">Embedding model</div>
          {isVoyage ? (
            <EmbedModelPicker
              models={models.embedding}
              model={model}
              dim={dim}
              onChange={(m, d) => {
                setModel(m)
                setDim(d)
                setTest(null)
              }}
              current={current}
              compatNote={models.compat_note}
              lockedModel={lockedModel}
              lockedDim={lockedDim}
            />
          ) : (
            <div className="info-line">
              This server embeds with <b className="mono">{server.embedder.type}</b> (<span className="mono">{server.embedder.model} · {server.embedder.dim}</span>).
              The Voyage catalog applies when the embedder is <span className="mono">voyage</span> — re-run the setup wizard to switch.
            </div>
          )}

          {changedEmbed && (
            <ReembedImpact models={models.embedding} current={current} next={{ model, dim }} rows={rows} contentChars={server.content_chars} />
          )}

          <div className="sub-head">Reranker</div>
          <RerankPicker
            models={models.rerank}
            value={rerank}
            onChange={(id) => {
              setRerank(id)
              setTest(null)
            }}
            current={currentRerank}
            locked={lockedRerank}
          />
          {!server.reranker.enabled && server.reranker.reason && (
            <div className="field-hint">Currently off: {server.reranker.reason}</div>
          )}

          {testErr && <ErrorBox>{testErr}</ErrorBox>}
          {test && <ModelTestResult result={test} embedModel={isVoyage ? model : current.model} rerankModel={rerank} />}

          <div className="form-actions sticky-actions">
            <button className="btn" onClick={runTest} disabled={testing}>
              {testing ? <Loader2 size={14} className="spin" /> : <Zap size={14} />} Test
            </button>
            <div className="spacer" />
            {dirty && <button className="btn ghost" onClick={reset}>Reset</button>}
            <button className="btn primary" onClick={apply} disabled={!dirty || saver.busy}>
              {saver.busy ? <Loader2 size={14} className="spin" /> : <Save size={14} />}
              {server.restart_supported ? 'Save & restart' : 'Save'}
            </button>
          </div>
          {dirty && impact === 'reembed' && (
            <div className="field-hint right">After the restart, re-embedding runs as a background job you can follow on the Overview.</div>
          )}
          <SaveResult res={saver.res} error={saver.error} restartNow={restartNow} />
          {config.voyage_model && config.voyage_model !== current.model && (
            <div className="field-hint">
              Saved config says <span className="mono">{config.voyage_model}</span> but the server runs <span className="mono">{current.model}</span> — a restart is pending or an environment variable overrides it.
            </div>
          )}
        </>
      )}
    </Section>
  )
}

// ---------------------------------------------------------------------------
// API keys
// ---------------------------------------------------------------------------

function KeyField({ label, settingKey, masked, value, onChange, envLocked, placeholder, extra }: {
  label: string
  settingKey: string
  masked: string
  value: string
  onChange: (v: string) => void
  envLocked: Set<string>
  placeholder: string
  extra?: ReactNode
}) {
  const locked = envLocked.has(settingKey)
  return (
    <div className="field key-field">
      <label>{label}</label>
      <div className="current-line">
        <span className="faint">Current</span>
        <span className="mono">{masked || 'not set'}</span>
        {locked && <EnvLock settingKey={settingKey} />}
      </div>
      {!locked && (
        <div className="key-input-row">
          <SecretInput value={value} onChange={onChange} placeholder={placeholder} />
          {extra}
        </div>
      )}
    </div>
  )
}

function KeysSection({ server, config, envLocked, save, restartNow }: SectionProps) {
  const [voyage, setVoyage] = useState('')
  const [openai, setOpenai] = useState('')
  const [qdrant, setQdrant] = useState('')
  const [verifying, setVerifying] = useState(false)
  const [verify, setVerify] = useState<ModelTestResponse | null>(null)
  const [verifyErr, setVerifyErr] = useState('')
  const saver = useSave(save)

  const showOpenai = config.embedder === 'openai'
  const showQdrant = config.vector_store === 'qdrant'
  const patch: ConfigUpdate = {}
  if (voyage.trim()) patch.voyage_api_key = voyage.trim()
  if (openai.trim()) patch.openai_api_key = openai.trim()
  if (qdrant.trim()) patch.qdrant_api_key = qdrant.trim()
  const dirty = Object.keys(patch).length > 0

  const verifyVoyage = async () => {
    setVerifying(true)
    setVerifyErr('')
    setVerify(null)
    try {
      setVerify(await api.testModels({
        voyage_api_key: voyage.trim(),
        embed_model: server.embedder.type === 'voyage' ? server.embedder.model : 'voyage-4',
        dim: server.embedder.type === 'voyage' ? server.embedder.dim : undefined,
      }))
    } catch (e) {
      setVerifyErr(errorText(e, 'Verification failed'))
    } finally {
      setVerifying(false)
    }
  }

  return (
    <Section id="keys" title="API keys" hint="stored in config.yaml (0600)">
      <KeyField
        label="Voyage AI API key"
        settingKey="voyage_api_key"
        masked={config.voyage_api_key}
        value={voyage}
        onChange={(v) => {
          setVoyage(v)
          setVerify(null)
        }}
        envLocked={envLocked}
        placeholder="paste a new key to replace (pa-…)"
        extra={
          <button className="btn" onClick={verifyVoyage} disabled={verifying} title={voyage ? 'Test the new key' : 'Test the saved key'}>
            {verifying ? <Loader2 size={14} className="spin" /> : <Zap size={14} />} Verify
          </button>
        }
      />
      {verifyErr && <ErrorBox>{verifyErr}</ErrorBox>}
      {verify && <TestResultRow label="Voyage key" detail={voyage ? 'new key' : 'saved key'} result={verify.embed} />}
      {!server.embedder.key_set && server.embedder.type === 'voyage' && (
        <WarnBox title="No Voyage key is set.">Indexing and search fail until you add one. Get a key at dash.voyageai.com.</WarnBox>
      )}

      {showOpenai && (
        <KeyField
          label="OpenAI-compatible API key"
          settingKey="openai_api_key"
          masked={config.openai_api_key}
          value={openai}
          onChange={setOpenai}
          envLocked={envLocked}
          placeholder="new key (empty for a local endpoint)"
        />
      )}
      {showQdrant && (
        <KeyField
          label="Qdrant API key"
          settingKey="qdrant_api_key"
          masked=""
          value={qdrant}
          onChange={setQdrant}
          envLocked={envLocked}
          placeholder="new Qdrant Cloud key"
        />
      )}

      <div className="form-actions">
        <div className="field-hint" style={{ margin: 0 }}>Leave a field empty to keep the current key.</div>
        <div className="spacer" />
        <button
          className="btn primary"
          disabled={!dirty || saver.busy}
          onClick={async () => {
            const r = await saver.run(patch)
            if (r) {
              setVoyage('')
              setOpenai('')
              setQdrant('')
            }
          }}
        >
          {saver.busy ? <Loader2 size={14} className="spin" /> : <Save size={14} />} Save keys
        </button>
      </div>
      <SaveResult res={saver.res} error={saver.error} restartNow={restartNow} />
    </Section>
  )
}

// ---------------------------------------------------------------------------
// Admin token
// ---------------------------------------------------------------------------

function TokenSection({ config, envLocked, onChanged }: { config: ConfigMasked; envLocked: Set<string>; onChanged: () => void }) {
  const locked = envLocked.has('admin_token')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [token, setNewToken] = useState('')
  const [note, setNote] = useState('')
  const [envOverride, setEnvOverride] = useState(false)
  const [confirm, setConfirm] = useState(false)

  const generate = async () => {
    setConfirm(false)
    setBusy(true)
    setError('')
    try {
      const r = await api.genToken()
      setNewToken(r.token)
      setNote(r.note)
      setEnvOverride(r.env_override)
      // No token yet: the server ignores the header until the token is active, so use it right away.
      // Replacing a token: switch over once the old one is rejected.
      if (!r.env_override) {
        if (config.admin_token_set) setPendingToken(r.token)
        else setToken(r.token)
      }
      onChanged()
    } catch (e) {
      setError(errorText(e, 'Could not generate a token'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Section id="token" title="Admin token" hint={config.admin_token_set ? 'set' : 'not set'}>
      <p className="lead">
        Protects <span className="mono">/api/*</span>, <span className="mono">/mcp</span> and this dashboard with a bearer token. Required when the server is reachable from the internet.
        Client machines use it in <span className="mono">enowx-rag connect --token …</span>.
      </p>
      <div className="current-line">
        <span className="faint">Current</span>
        <span className="mono">{config.admin_token_set ? config.admin_token || 'set' : 'none — the server is open'}</span>
        {locked && <EnvLock settingKey="admin_token" />}
      </div>
      {!locked && (
        <div className="form-actions">
          <button className="btn primary" onClick={() => (config.admin_token_set ? setConfirm(true) : generate())} disabled={busy}>
            {busy ? <Loader2 size={14} className="spin" /> : <RefreshCw size={14} />}
            {config.admin_token_set ? 'Generate a new token' : 'Generate token'}
          </button>
        </div>
      )}
      {error && <ErrorBox>{error}</ErrorBox>}
      {token && (
        <div className="token-result">
          <CopyBlock title="new admin token — copy it now, it is shown once" code={token} secret={token} />
          {note && <div className="field-hint">{note}</div>}
          {envOverride ? (
            <WarnBox title="RAG_ADMIN_TOKEN is set in the environment">
              and takes precedence over this saved token. Unset it to use the generated one.
            </WarnBox>
          ) : (
            <div className="field-hint">
              This browser switches to the new token automatically once the server accepts it. Update connected machines with
              <span className="mono"> enowx-rag connect --remote {window.location.origin} --token …</span>
            </div>
          )}
        </div>
      )}
      {confirm && (
        <ConfirmDialog
          title="Replace the admin token?"
          confirmLabel="Generate new token"
          onCancel={() => setConfirm(false)}
          onConfirm={generate}
        >
          <p>Every connected machine and agent using the old token loses access until you update it with the new one.</p>
        </ConfirmDialog>
      )}
    </Section>
  )
}

// ---------------------------------------------------------------------------
// Auto-sync
// ---------------------------------------------------------------------------

const SYNC_PRESETS = [
  { v: 'off', label: 'Off' },
  { v: '30s', label: '30 s' },
  { v: '60s', label: '1 min' },
  { v: '5m', label: '5 min' },
  { v: '15m', label: '15 min' },
]

function AutoSyncSection({ server, config, envLocked, save, restartNow }: SectionProps) {
  const locked = envLocked.has('auto_sync_interval')
  const initial = config.auto_sync_interval || (server.auto_sync.enabled ? server.auto_sync.interval : 'off')
  const [value, setValue] = useState(initial)
  const saver = useSave(save)
  const valid = /^(off|0|false|no|disabled|\d+|(\d+(\.\d+)?(ms|s|m|h))+)$/i.test(value.trim())

  return (
    <Section id="autosync" title="Auto-sync" hint={server.auto_sync.enabled ? `every ${server.auto_sync.interval}` : 'off'}>
      <p className="lead">
        How stale a project's index may get before the next search re-syncs changed files first. Client machines scan their own
        folders; the server re-syncs folders that live on it. Shorter = fresher results, slightly slower first search after edits.
      </p>
      {locked ? (
        <div className="current-line">
          <span className="faint">Current</span>
          <span className="mono">{server.auto_sync.enabled ? server.auto_sync.interval : 'off'}</span>
          <EnvLock settingKey="auto_sync_interval" />
        </div>
      ) : (
        <>
          <div className="preset-row">
            {SYNC_PRESETS.map((p) => (
              <button key={p.v} type="button" className={`chip-btn ${value === p.v ? 'active' : ''}`} onClick={() => setValue(p.v)}>
                {p.label}
              </button>
            ))}
            <input
              className="input mono small-input"
              value={value}
              onChange={(e) => setValue(e.target.value)}
              aria-label="Auto-sync interval"
              placeholder="e.g. 90s"
            />
          </div>
          {!valid && <div className="field-hint crit-text">Use a duration like 60s, 5m, a number of seconds, or off.</div>}
          <div className="form-actions">
            <div className="spacer" />
            <button className="btn primary" disabled={!valid || value === initial || saver.busy} onClick={() => saver.run({ auto_sync_interval: value.trim() })}>
              {saver.busy ? <Loader2 size={14} className="spin" /> : <Save size={14} />} Save
            </button>
          </div>
          <SaveResult res={saver.res} error={saver.error} restartNow={restartNow} />
        </>
      )}
    </Section>
  )
}

// ---------------------------------------------------------------------------
// Database
// ---------------------------------------------------------------------------

function InfoRow({ k, children }: { k: string; children: ReactNode }) {
  return (
    <div className="stat-line">
      <span className="k">{k}</span>
      <span className="v">{children}</span>
    </div>
  )
}

function DatabaseSection({ server, config, envLocked, save, restartNow }: SectionProps) {
  const s = server.store
  const dsnLocked = envLocked.has('pgvector_dsn')
  const [dsn, setDsn] = useState('')
  const [testing, setTesting] = useState(false)
  const [test, setTest] = useState<SetupTestResponse | null>(null)
  const [testErr, setTestErr] = useState('')
  const saver = useSave(save)

  const runTest = async () => {
    setTesting(true)
    setTestErr('')
    setTest(null)
    try {
      setTest(await api.setupTest({ vector_store: 'pgvector', embedder: config.embedder || 'voyage', pgvector_dsn: dsn.trim() }))
    } catch (e) {
      setTestErr(errorText(e, 'Test failed'))
    } finally {
      setTesting(false)
    }
  }

  return (
    <Section id="database" title="Database" hint={<span className={s.ok ? 'good-text' : 'crit-text'}>{s.ok ? '● connected' : '● error'}</span>}>
      <div className="info-grid">
        <InfoRow k="Store"><span className="mono">{s.type}</span>{envLocked.has('vector_store') && <> <EnvLock settingKey="vector_store" compact /></>}</InfoRow>
        <InfoRow k="Target"><span className="mono break">{s.target || '—'}</span></InfoRow>
        {s.server_version && <InfoRow k="Server"><span className="mono">{s.server_version}</span></InfoRow>}
        {s.extension_version && <InfoRow k="pgvector"><span className="mono">{s.extension_version}</span></InfoRow>}
        {s.table && <InfoRow k="Table"><span className="mono">{s.table}{s.dim ? ` · vector(${s.dim})` : ''}</span></InfoRow>}
        {s.rows !== undefined && <InfoRow k="Rows"><span className="mono">{formatNumber(s.rows)}</span></InfoRow>}
        {s.size_bytes !== undefined && <InfoRow k="Size"><span className="mono">{formatBytes(s.size_bytes)}</span></InfoRow>}
        {s.latency_ms !== undefined && <InfoRow k="Latency"><span className="mono">{formatMs(s.latency_ms)}</span></InfoRow>}
        {config.sqlite_path && s.type === 'sqlite' && <InfoRow k="File"><span className="mono break">{config.sqlite_path}</span></InfoRow>}
      </div>
      {s.error && <ErrorBox>{s.error}</ErrorBox>}

      {s.type === 'pgvector' && (
        dsnLocked ? (
          <div className="current-line"><span className="faint">DSN</span><span className="mono break">{config.pgvector_dsn || s.target}</span><EnvLock settingKey="pgvector_dsn" /></div>
        ) : (
          <div className="field" style={{ marginTop: 16 }}>
            <label>Change connection string (DSN)</label>
            <div className="key-input-row">
              <input
                className="input mono"
                value={dsn}
                onChange={(e) => {
                  setDsn(e.target.value)
                  setTest(null)
                }}
                placeholder={config.pgvector_dsn || 'postgresql://user:password@host:5432/db?sslmode=disable'}
                autoComplete="off"
                spellCheck={false}
              />
              <button className="btn" onClick={runTest} disabled={!dsn.trim() || testing}>
                {testing ? <Loader2 size={14} className="spin" /> : <Zap size={14} />} Test
              </button>
            </div>
            <div className="field-hint">The new database starts empty unless it already holds enowx-rag data. The password is never shown again after saving.</div>
            {testErr && <ErrorBox>{testErr}</ErrorBox>}
            {test && <TestResultRow label="PostgreSQL" detail="pgvector" result={test.vector_store} />}
            <div className="form-actions">
              <div className="spacer" />
              <button className="btn primary" disabled={!dsn.trim() || saver.busy} onClick={() => saver.run({ pgvector_dsn: dsn.trim(), restart: server.restart_supported })}>
                {saver.busy ? <Loader2 size={14} className="spin" /> : <Save size={14} />}
                {server.restart_supported ? 'Save & restart' : 'Save'}
              </button>
            </div>
            <SaveResult res={saver.res} error={saver.error} restartNow={restartNow} />
          </div>
        )
      )}
      {s.type !== 'pgvector' && (
        <div className="field-hint">
          To move to PostgreSQL + pgvector, re-run the setup wizard, then bring the data over with Import or <span className="mono">enowx-rag push</span>.
        </div>
      )}
    </Section>
  )
}

// ---------------------------------------------------------------------------
// Server
// ---------------------------------------------------------------------------

function ServerSection({ server, restartNow, onRunWizard }: { server: ServerInfo; restartNow: () => Promise<void>; onRunWizard: () => void }) {
  const [confirm, setConfirm] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  return (
    <Section id="server" title="Server">
      <div className="info-grid">
        <InfoRow k="Version"><span className="mono">{server.version}</span></InfoRow>
        <InfoRow k="Uptime"><span className="mono" title={`started ${fullTime(server.started_at)}`}>{formatDuration(server.uptime_s)}</span></InfoRow>
        <InfoRow k="Host"><span className="mono">{server.hostname}</span></InfoRow>
        <InfoRow k="OS / Go"><span className="mono">{server.os} · {server.go_version}</span></InfoRow>
        <InfoRow k="Config file"><span className="mono break">{server.config_path}</span></InfoRow>
        <InfoRow k="Boot id"><span className="mono">{server.boot_id}</span></InfoRow>
      </div>
      {server.env_locked.length > 0 && (
        <div className="field-hint">
          Fixed by environment: {server.env_locked.map((k) => <span key={k} className="tag mono env-tag">{k}</span>)}
        </div>
      )}
      {error && <ErrorBox>{error}</ErrorBox>}
      <div className="form-actions">
        <button className="btn" onClick={onRunWizard}>
          <Wand2 size={14} /> Re-run setup wizard
        </button>
        <div className="spacer" />
        {server.restart_supported ? (
          <button className="btn" onClick={() => setConfirm(true)}>
            <Power size={14} /> Restart server
          </button>
        ) : (
          <span className="field-hint" style={{ margin: 0 }}>Restart from the dashboard isn't supported here — restart the process/container yourself.</span>
        )}
      </div>
      {confirm && (
        <ConfirmDialog
          title="Restart the server?"
          confirmLabel="Restart"
          busy={busy}
          error={error}
          onCancel={() => setConfirm(false)}
          onConfirm={async () => {
            setBusy(true)
            setError('')
            try {
              await restartNow()
              setConfirm(false)
            } catch (e) {
              setError(errorText(e, 'Restart failed'))
            } finally {
              setBusy(false)
            }
          }}
        >
          <p>Searches and syncs from connected machines pause for a few seconds; running jobs may be interrupted.</p>
        </ConfirmDialog>
      )}
    </Section>
  )
}
