import { useState } from 'react'
import { ChevronLeft, ChevronRight, Check, Database, HardDrive, Loader2, Zap } from 'lucide-react'
import { api, errorText, type ComponentResult } from '../../lib/api'
import { Badge, EnvLock, ErrorBox, SuccessBox, TestResultRow } from '../../components/ui'
import { envLockedSet, storeFromEnv, type StepProps } from './types'

const CHOICES = [
  {
    id: 'pgvector',
    name: 'PostgreSQL + pgvector',
    icon: Database,
    desc: 'Recommended for a server. Hybrid search (vector + full-text), concurrent clients, easy backups.',
    meta: 'hybrid · HNSW · tsvector',
    recommended: true,
  },
  {
    id: 'sqlite',
    name: 'Built-in SQLite',
    icon: HardDrive,
    desc: 'Zero setup: a single file on this server. Fine for one user; switch to pgvector later via Import.',
    meta: 'single file · no extra service',
    recommended: false,
  },
] as const

export function StepDatabase({ cfg, updateCfg, env, onBack, onNext }: StepProps) {
  const [testing, setTesting] = useState(false)
  const [result, setResult] = useState<ComponentResult | null>(null)
  const [error, setError] = useState('')
  const d = env.detect
  const locked = envLockedSet(env)
  const fromEnv = storeFromEnv(env)

  // Store fixed by the environment and reachable → nothing to do here.
  if (fromEnv && d) {
    return (
      <div className="card">
        <div className="card-head">
          <h2>Database</h2>
          <span className="step-badge mono">2 / 6</span>
        </div>
        <div className="card-body">
          {d.store.ok ? (
            <SuccessBox><b>Already configured by the environment.</b> Nothing to choose here.</SuccessBox>
          ) : (
            <ErrorBox>
              The database is set by the environment but is not reachable: {d.store.message}. Fix the server's environment
              (e.g. the database container) — you can continue and re-test in the Review step.
            </ErrorBox>
          )}
          <div className="summary-box" style={{ marginTop: 14 }}>
            <div className="summary-row"><span className="sk">Store</span><span className="sv mono">{d.store.type}</span></div>
            {d.store.dsn_masked && <div className="summary-row"><span className="sk">Connection</span><span className="sv mono">{d.store.dsn_masked}</span></div>}
            {d.store.server_version && <div className="summary-row"><span className="sk">Server</span><span className="sv mono">{d.store.server_version}</span></div>}
            {d.store.extension_version && <div className="summary-row"><span className="sk">pgvector</span><span className="sv mono">{d.store.extension_version}</span></div>}
            <div className="summary-row"><span className="sk">Source</span><span className="sv"><EnvLock settingKey={locked.has('pgvector_dsn') ? 'pgvector_dsn' : 'vector_store'} /></span></div>
          </div>
        </div>
        <div className="nav-buttons">
          <button className="btn" onClick={onBack}><ChevronLeft size={14} /> Back</button>
          <div className="spacer" />
          <button className="btn primary" onClick={onNext}>Continue <ChevronRight size={14} /></button>
        </div>
      </div>
    )
  }

  const test = async () => {
    setTesting(true)
    setError('')
    setResult(null)
    try {
      const r = await api.setupTest({
        vector_store: cfg.vectorStore,
        embedder: cfg.embedder,
        pgvector_dsn: cfg.vectorStore === 'pgvector' ? cfg.pgvectorDSN.trim() : undefined,
        sqlite_path: cfg.vectorStore === 'sqlite' ? cfg.sqlitePath.trim() || undefined : undefined,
      })
      setResult(r.vector_store)
    } catch (e) {
      setError(errorText(e, 'Test failed'))
    } finally {
      setTesting(false)
    }
  }

  const dsnMissing = cfg.vectorStore === 'pgvector' && !cfg.pgvectorDSN.trim() && !locked.has('pgvector_dsn')
  const canProceed = (cfg.vectorStore === 'pgvector' || cfg.vectorStore === 'sqlite') && !dsnMissing

  return (
    <div className="card">
      <div className="card-head">
        <h2>Choose the database</h2>
        <span className="step-badge mono">2 / 6</span>
      </div>
      <div className="card-body">
        <p>Where this server stores chunks and vectors.</p>
        <div className="cards cards-2">
          {CHOICES.map((c) => {
            const Icon = c.icon
            const sel = cfg.vectorStore === c.id
            return (
              <button
                type="button"
                key={c.id}
                className={`pcard ${sel ? 'selected' : ''}`}
                onClick={() => {
                  updateCfg({ vectorStore: c.id })
                  setResult(null)
                }}
              >
                {sel && <Check className="pcard-check" size={16} />}
                <div className="pcard-icon"><Icon size={18} strokeWidth={1.5} /></div>
                <div className="pname">{c.name} {c.recommended && <Badge tone="accent">recommended</Badge>}</div>
                <div className="pdesc">{c.desc}</div>
                <div className="pmeta mono">{c.meta}</div>
              </button>
            )
          })}
        </div>

        {cfg.vectorStore === 'pgvector' && (
          <div className="field">
            <label>Connection string (DSN)</label>
            {locked.has('pgvector_dsn') ? (
              <div className="current-line"><EnvLock settingKey="pgvector_dsn" /></div>
            ) : (
              <input
                className="input mono"
                type="text"
                value={cfg.pgvectorDSN}
                onChange={(e) => {
                  updateCfg({ pgvectorDSN: e.target.value })
                  setResult(null)
                }}
                placeholder="postgresql://enowx:password@localhost:5432/enowxrag?sslmode=disable"
                autoComplete="off"
                spellCheck={false}
              />
            )}
            <div className="field-hint">
              The database must have the <span className="mono">vector</span> extension available (the <span className="mono">pgvector/pgvector</span> Docker image has it).
              The test connects, runs <span className="mono">CREATE EXTENSION IF NOT EXISTS vector</span> and reports the versions. The DSN is not kept in this browser.
            </div>
          </div>
        )}

        {cfg.vectorStore === 'sqlite' && (
          <div className="field">
            <label>Database file <span className="faint">(optional)</span></label>
            {locked.has('sqlite_path') ? (
              <div className="current-line"><EnvLock settingKey="sqlite_path" /></div>
            ) : (
              <input
                className="input mono"
                value={cfg.sqlitePath}
                onChange={(e) => updateCfg({ sqlitePath: e.target.value })}
                placeholder="~/.enowx-rag/rag.db (default)"
              />
            )}
            <div className="field-hint">Leave empty for the default location on this server.</div>
          </div>
        )}

        <button className="btn" onClick={test} disabled={testing || dsnMissing}>
          {testing ? <Loader2 size={14} className="spin" /> : <Zap size={14} />} Test connection
        </button>
        <div style={{ marginTop: 12 }}>
          {error && <ErrorBox>{error}</ErrorBox>}
          <TestResultRow label="Database" detail={cfg.vectorStore} result={result} />
        </div>
      </div>
      <div className="nav-buttons">
        <button className="btn" onClick={onBack}><ChevronLeft size={14} /> Back</button>
        <div className="spacer" />
        <button className="btn primary" onClick={onNext} disabled={!canProceed}>
          Next <ChevronRight size={14} />
        </button>
      </div>
    </div>
  )
}
