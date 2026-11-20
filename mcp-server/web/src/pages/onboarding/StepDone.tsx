import { Check, ChevronLeft, LayoutGrid } from 'lucide-react'
import { formatNumber } from '../../lib/format'
import type { StepProps } from './types'

export function StepDone({ cfg, env, onBack, onFinish }: StepProps & { onFinish: () => void }) {
  const sqlite = env.detect?.sqlite_store
  return (
    <div className="card">
      <div className="card-head">
        <h2>All set</h2>
        <span className="step-badge mono">6 / 6</span>
      </div>
      <div className="card-body done-body">
        <div className="done-icon"><Check size={28} strokeWidth={2.5} /></div>
        <div className="done-title">Your server is ready</div>
        <div className="done-sub">
          Projects appear on the dashboard as soon as a connected machine's agent searches them for the first time.
        </div>

        <div className="summary-box">
          <div className="summary-row"><span className="sk">Database</span><span className="sv mono">{env.detect?.store.from_env ? env.detect.store.type : cfg.vectorStore}</span></div>
          <div className="summary-row">
            <span className="sk">Embedding</span>
            <span className="sv mono">{cfg.embedder === 'voyage' ? `${cfg.voyageModel} · ${cfg.voyageDim}` : cfg.embedder}</span>
          </div>
          <div className="summary-row"><span className="sk">Reranker</span><span className="sv mono">{cfg.rerankerModel || 'off'}</span></div>
        </div>

        {sqlite && sqlite.points > 0 && cfg.vectorStore !== 'sqlite' && (
          <p style={{ fontSize: 12.5 }}>
            This server also has an older SQLite store (<span className="mono">{sqlite.path}</span>, {formatNumber(sqlite.points)} chunks).
            The server imports it into the new database by itself once it runs with the new settings — follow it in the
            Jobs panel. The file is then kept as <span className="mono">rag.db.imported-…</span>. Set{' '}
            <span className="mono">RAG_AUTO_IMPORT_SQLITE=false</span> to skip that.
          </p>
        )}
      </div>
      <div className="nav-buttons">
        <button className="btn" onClick={onBack}><ChevronLeft size={14} /> Back</button>
        <div className="spacer" />
        <button className="btn primary" onClick={onFinish}>
          <LayoutGrid size={14} /> Open dashboard
        </button>
      </div>
    </div>
  )
}
