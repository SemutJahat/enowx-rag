import { useState } from 'react'
import { ChevronLeft, ChevronRight, ChevronDown, Loader2, Zap, Check } from 'lucide-react'
import { api, errorText, type ModelTestResponse } from '../../lib/api'
import { EmbedModelPicker, ModelTestResult, RerankPicker } from '../../components/ModelPicker'
import { EnvLock, ErrorBox, SecretInput, SuccessBox } from '../../components/ui'
import { envLockedSet, voyageKeyAvailable, type EmbedderProvider, type StepProps } from './types'

export function StepModels({ cfg, updateCfg, env, onBack, onNext }: StepProps) {
  const [testing, setTesting] = useState(false)
  const [test, setTest] = useState<ModelTestResponse | null>(null)
  const [error, setError] = useState('')
  const [otherOpen, setOtherOpen] = useState(cfg.embedder !== 'voyage')
  const d = env.detect
  const locked = envLockedSet(env)
  const models = env.models
  const keyFromEnv = !!d?.voyage_key_from_env || locked.has('voyage_api_key')
  const keySaved = !!d?.voyage_key_set
  const isVoyage = cfg.embedder === 'voyage'

  const verify = async () => {
    setTesting(true)
    setError('')
    setTest(null)
    try {
      setTest(await api.testModels({
        voyage_api_key: cfg.voyageAPIKey.trim() || undefined,
        embed_model: cfg.voyageModel,
        dim: cfg.voyageDim,
        rerank_model: cfg.rerankerModel || undefined,
      }))
    } catch (e) {
      setError(errorText(e, 'Verification failed'))
    } finally {
      setTesting(false)
    }
  }

  const setEmbedder = (e: EmbedderProvider) => {
    updateCfg({ embedder: e })
    setTest(null)
  }

  const canProceed = isVoyage
    ? voyageKeyAvailable(env, cfg)
    : cfg.embedder === 'openai'
      ? cfg.openaiModel.trim() !== '' && cfg.openaiBaseURL.trim() !== ''
      : cfg.teiURL.trim() !== ''

  return (
    <div className="card">
      <div className="card-head">
        <h2>Choose models</h2>
        <span className="step-badge mono">3 / 6</span>
        <span className="card-hint">Voyage AI</span>
      </div>
      <div className="card-body">
        {isVoyage && (
          <>
            <div className="field">
              <label>Voyage API key</label>
              {keyFromEnv ? (
                <SuccessBox>Set by environment variable <span className="mono">RAG_VOYAGE_API_KEY</span> — nothing to enter.</SuccessBox>
              ) : (
                <>
                  <div className="key-input-row">
                    <SecretInput
                      value={cfg.voyageAPIKey}
                      onChange={(v) => {
                        updateCfg({ voyageAPIKey: v })
                        setTest(null)
                      }}
                      placeholder={keySaved ? 'a key is already saved — paste a new one to replace it' : 'pa-…'}
                    />
                    <button className="btn" onClick={verify} disabled={testing || !voyageKeyAvailable(env, cfg)}>
                      {testing ? <Loader2 size={14} className="spin" /> : <Zap size={14} />} Verify
                    </button>
                  </div>
                  <div className="field-hint">
                    Create one at <span className="mono">dash.voyageai.com</span>. New accounts get free tokens per model (shown below). The key is sent to this server only and never stored in the browser.
                  </div>
                </>
              )}
            </div>
            {keyFromEnv && (
              <div className="form-actions" style={{ marginTop: -6, marginBottom: 12 }}>
                <button className="btn" onClick={verify} disabled={testing}>
                  {testing ? <Loader2 size={14} className="spin" /> : <Zap size={14} />} Test models
                </button>
              </div>
            )}
            {error && <ErrorBox>{error}</ErrorBox>}
            {test && <ModelTestResult result={test} embedModel={cfg.voyageModel} rerankModel={cfg.rerankerModel} />}

            <div className="sub-head">Embedding model</div>
            {models ? (
              <EmbedModelPicker
                models={models.embedding}
                model={cfg.voyageModel}
                dim={cfg.voyageDim}
                onChange={(m, dim) => {
                  updateCfg({ voyageModel: m, voyageDim: dim })
                  setTest(null)
                }}
                compatNote={models.compat_note}
                lockedModel={locked.has('voyage_model')}
                lockedDim={locked.has('voyage_dim')}
              />
            ) : (
              <div className="field-row">
                <div className="field">
                  <label>Model</label>
                  <input className="input mono" value={cfg.voyageModel} onChange={(e) => updateCfg({ voyageModel: e.target.value })} />
                </div>
                <div className="field">
                  <label>Dimensions</label>
                  <input className="input mono" type="number" value={cfg.voyageDim} onChange={(e) => updateCfg({ voyageDim: parseInt(e.target.value) || 1024 })} />
                </div>
              </div>
            )}

            <div className="sub-head">Reranker</div>
            {models ? (
              <RerankPicker
                models={models.rerank}
                value={cfg.rerankerModel}
                onChange={(id) => {
                  updateCfg({ rerankerModel: id })
                  setTest(null)
                }}
                locked={locked.has('reranker_model')}
              />
            ) : (
              <div className="field">
                <input className="input mono" value={cfg.rerankerModel} placeholder="rerank-3 (empty = off)" onChange={(e) => updateCfg({ rerankerModel: e.target.value })} />
              </div>
            )}
          </>
        )}

        {/* Other embedders, collapsed by default */}
        <button type="button" className="collapse-head" onClick={() => setOtherOpen((o) => !o)} aria-expanded={otherOpen}>
          {otherOpen ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
          Other embedder (OpenAI-compatible, self-hosted TEI)
          {locked.has('embedder') && <EnvLock settingKey="embedder" compact />}
        </button>
        {otherOpen && (
          <div className="collapse-body">
            <div className="pill-group" style={{ marginBottom: 14 }}>
              {(['voyage', 'openai', 'tei'] as EmbedderProvider[]).map((e) => (
                <button
                  type="button"
                  key={e}
                  className={`pill ${cfg.embedder === e ? 'active' : ''}`}
                  onClick={() => !locked.has('embedder') && setEmbedder(e)}
                  disabled={locked.has('embedder') && cfg.embedder !== e}
                >
                  {cfg.embedder === e && <Check size={12} />} {e === 'voyage' ? 'Voyage AI' : e === 'openai' ? 'OpenAI-compatible' : 'TEI'}
                </button>
              ))}
            </div>

            {cfg.embedder === 'openai' && (
              <>
                <div className="field">
                  <label>Base URL</label>
                  <input className="input mono" value={cfg.openaiBaseURL} onChange={(e) => updateCfg({ openaiBaseURL: e.target.value })} placeholder="https://api.openai.com/v1" />
                  <div className="field-hint">Any <span className="mono">/v1/embeddings</span> API: OpenAI, Together, Jina, Mistral, Ollama at <span className="mono">http://localhost:11434/v1</span>, LiteLLM…</div>
                </div>
                <div className="field">
                  <label>API key <span className="faint">(empty for local / no auth)</span></label>
                  <SecretInput value={cfg.openaiAPIKey} onChange={(v) => updateCfg({ openaiAPIKey: v })} placeholder="sk-…" />
                </div>
                <div className="field-row">
                  <div className="field">
                    <label>Model</label>
                    <input className="input mono" value={cfg.openaiModel} onChange={(e) => updateCfg({ openaiModel: e.target.value })} placeholder="text-embedding-3-small" />
                  </div>
                  <div className="field">
                    <label>Dimensions <span className="faint">(0 = auto)</span></label>
                    <input className="input mono" type="number" min={0} max={4096} step={128} value={cfg.openaiDim} onChange={(e) => updateCfg({ openaiDim: parseInt(e.target.value) || 0 })} />
                  </div>
                </div>
              </>
            )}

            {cfg.embedder === 'tei' && (
              <div className="field">
                <label>TEI server URL</label>
                <input className="input mono" value={cfg.teiURL} onChange={(e) => updateCfg({ teiURL: e.target.value })} placeholder="http://localhost:8081" />
                <div className="field-hint">Text Embeddings Inference running any local model (BGE, GTE, E5, nomic…), reachable from this server.</div>
              </div>
            )}

            {!isVoyage && (
              <div className="field-hint">
                Reranking uses Voyage and needs a Voyage key{voyageKeyAvailable(env, cfg) ? ` — ${cfg.rerankerModel || 'off'} will be used.` : '; without one it stays off.'}
              </div>
            )}
          </div>
        )}
      </div>
      <div className="nav-buttons">
        <button className="btn" onClick={onBack}><ChevronLeft size={14} /> Back</button>
        {isVoyage && !voyageKeyAvailable(env, cfg) && <span className="gate-info">Enter the Voyage key to continue</span>}
        <div className="spacer" />
        <button className="btn primary" onClick={onNext} disabled={!canProceed}>
          Next <ChevronRight size={14} />
        </button>
      </div>
    </div>
  )
}
