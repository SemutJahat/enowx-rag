import { useCallback, useEffect, useState } from 'react'
import { ChevronLeft, Loader2, Power, RotateCcw } from 'lucide-react'
import { api, errorText, type SetupApplyRequest, type SetupTestResponse } from '../../lib/api'
import { ErrorBox, SuccessBox, TestResultRow, WarnBox } from '../../components/ui'
import { draftToRequest, envLockedSet, storeFromEnv, type StepProps } from './types'

export function StepReview({ cfg, env, onBack, onApply }: StepProps & { onApply: (req: SetupApplyRequest) => Promise<void> }) {
  const [testing, setTesting] = useState(false)
  const [result, setResult] = useState<SetupTestResponse | null>(null)
  const [testErr, setTestErr] = useState('')
  const [applying, setApplying] = useState(false)
  const [applyErr, setApplyErr] = useState('')
  const req = draftToRequest(cfg, env)
  const locked = envLockedSet(env)
  const fromEnv = storeFromEnv(env)
  const dsnMissing = !fromEnv && cfg.vectorStore === 'pgvector' && !cfg.pgvectorDSN.trim() && !locked.has('pgvector_dsn')

  const runTest = useCallback(async () => {
    setTesting(true)
    setTestErr('')
    setResult(null)
    try {
      setResult(await api.setupTest(draftToRequest(cfg, env)))
    } catch (e) {
      setTestErr(errorText(e, 'Test failed'))
    } finally {
      setTesting(false)
    }
  }, [cfg, env])

  // Test automatically when the step opens.
  useEffect(() => {
    if (!dsnMissing) runTest()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const apply = async () => {
    setApplying(true)
    setApplyErr('')
    try {
      await onApply(req)
    } catch (e) {
      setApplyErr(errorText(e, 'Could not apply the configuration'))
    } finally {
      setApplying(false)
    }
  }

  const passed = !!result && result.vector_store.ok && result.embedder.ok && (!result.reranker || result.reranker.ok)
  const failed = !!result && !passed
  const restartSupported = env.status?.restart_supported !== false

  const storeLabel = fromEnv && env.detect ? `${env.detect.store.type} (environment)` : cfg.vectorStore
  const embedLabel =
    cfg.embedder === 'voyage' ? `${cfg.voyageModel} · ${cfg.voyageDim}-dim` : cfg.embedder === 'openai' ? `${cfg.openaiModel} @ ${cfg.openaiBaseURL}` : `TEI @ ${cfg.teiURL}`

  return (
    <div className="card">
      <div className="card-head">
        <h2>Review &amp; apply</h2>
        <span className="step-badge mono">4 / 6</span>
        <span className="card-hint">live connection test</span>
      </div>
      <div className="card-body">
        {dsnMissing && (
          <WarnBox title="The database connection string is missing.">
            It isn't kept in the browser after a reload — go back to Database and enter it again.
          </WarnBox>
        )}

        <div className="summary-box">
          <div className="summary-row"><span className="sk">Database</span><span className="sv mono">{storeLabel}</span></div>
          {!fromEnv && cfg.vectorStore === 'pgvector' && cfg.pgvectorDSN && (
            <div className="summary-row"><span className="sk">DSN</span><span className="sv mono">{cfg.pgvectorDSN.replace(/:\/\/([^:@/]+):[^@]*@/, '://$1:••••@')}</span></div>
          )}
          <div className="summary-row"><span className="sk">Embedder</span><span className="sv mono">{cfg.embedder} · {embedLabel}</span></div>
          <div className="summary-row"><span className="sk">Reranker</span><span className="sv mono">{req.reranker_model === undefined ? 'set by environment' : req.reranker_model || 'off'}</span></div>
          {cfg.embedder === 'voyage' && (
            <div className="summary-row">
              <span className="sk">Voyage key</span>
              <span className="sv mono">
                {env.detect?.voyage_key_from_env ? 'environment' : cfg.voyageAPIKey ? '••••' + cfg.voyageAPIKey.slice(-4) : env.detect?.voyage_key_set ? 'saved key' : 'missing'}
              </span>
            </div>
          )}
          <div className="summary-row"><span className="sk">Config file</span><span className="sv mono">{env.status?.config_path || '~/.enowx-rag/config.yaml'}</span></div>
        </div>

        <div className="review-tests">
          <div className="field-label row-between">
            <span>Connection test</span>
            <button className="btn small" onClick={runTest} disabled={testing || dsnMissing}>
              {testing ? <Loader2 size={13} className="spin" /> : <RotateCcw size={13} />} {result ? 'Re-run' : 'Run'}
            </button>
          </div>
          {testing && !result && <div className="inline-loading"><Loader2 size={15} className="spin" /> Testing database, embedder and reranker…</div>}
          {testErr && <ErrorBox>{testErr}</ErrorBox>}
          {result && (
            <>
              <TestResultRow label="Database" detail={storeLabel} result={result.vector_store} />
              <TestResultRow label="Embedder" detail={embedLabel} result={result.embedder} />
              {result.reranker && <TestResultRow label="Reranker" detail={req.reranker_model || ''} result={result.reranker} />}
            </>
          )}
          {passed && <SuccessBox>All checks passed. Apply to save the configuration{restartSupported ? ' and restart the server' : ''}.</SuccessBox>}
          {failed && (
            <WarnBox title="Some checks failed.">
              Fix them (go back) and re-run, or apply anyway if you know the component will be available after the restart.
            </WarnBox>
          )}
        </div>

        {!restartSupported && (
          <div className="field-hint">This server can't restart itself — after applying, restart the process or container to load the new settings.</div>
        )}
        {applyErr && <ErrorBox>{applyErr}</ErrorBox>}
      </div>
      <div className="nav-buttons">
        <button className="btn" onClick={onBack} disabled={applying}><ChevronLeft size={14} /> Back</button>
        <div className="spacer" />
        <button className="btn primary" onClick={apply} disabled={applying || testing || dsnMissing || (!result && !testErr)}>
          {applying ? <Loader2 size={14} className="spin" /> : <Power size={14} />}
          {failed || testErr ? 'Apply anyway' : restartSupported ? 'Apply & restart' : 'Apply'}
        </button>
      </div>
    </div>
  )
}
