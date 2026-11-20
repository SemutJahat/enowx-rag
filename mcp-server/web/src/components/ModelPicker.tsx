// Rich Voyage model pickers (embedding + rerank), the re-embed impact box and
// the model test result. Shared by Settings and the onboarding wizard.

import { useState } from 'react'
import { ArrowRight, Check, ChevronDown, ChevronRight, Info } from 'lucide-react'
import type { ModelInfo, ModelTestResponse, RerankInfo } from '../lib/api'
import { estimateCost, estimateTokens, formatMTok, formatNumber, formatPrice, formatTokens, formatUSD } from '../lib/format'
import { Badge, EnvLock, SuccessBox, TestResultRow, WarnBox } from './ui'

function StatusBadge({ status }: { status: 'latest' | 'older' | 'deprecated' }) {
  if (status === 'latest') return <Badge tone="good">latest</Badge>
  if (status === 'deprecated') return <Badge tone="crit">deprecated</Badge>
  return <Badge tone="muted">older</Badge>
}

function Spec({ k, v }: { k: string; v: string }) {
  return (
    <span className="spec">
      <span className="spec-k">{k}</span>
      <span className="spec-v mono">{v}</span>
    </span>
  )
}

// ---------------------------------------------------------------------------
// Embedding model picker
// ---------------------------------------------------------------------------

interface EmbedPickerProps {
  models: ModelInfo[]
  model: string
  dim: number
  onChange: (model: string, dim: number) => void
  /** What the server runs now (badged "current"). */
  current?: { model: string; dim: number }
  compatNote?: string
  /** Env-locked keys: voyage_model and/or voyage_dim. */
  lockedModel?: boolean
  lockedDim?: boolean
  disabled?: boolean
}

export function EmbedModelPicker({ models, model, dim, onChange, current, compatNote, lockedModel, lockedDim, disabled }: EmbedPickerProps) {
  const [showOld, setShowOld] = useState(false)
  const selected = models.find((m) => m.id === model)
  const visible = models.filter(
    (m) => showOld || m.status !== 'deprecated' || m.id === model || m.id === current?.model,
  )
  const hiddenCount = models.length - visible.length
  const pickDisabled = disabled || lockedModel

  const pick = (m: ModelInfo) => {
    if (pickDisabled) return
    const nextDim = m.dims.includes(dim) ? dim : m.default_dim
    onChange(m.id, nextDim)
  }

  if (models.length === 0) {
    return <div className="panel-empty">The model catalog is not available (GET /api/models failed).</div>
  }

  return (
    <div className="model-picker">
      {lockedModel && <div className="lock-line"><EnvLock settingKey="voyage_model" /></div>}
      <div className="model-list" role="radiogroup" aria-label="Embedding model">
        {visible.map((m) => {
          const isSel = m.id === model
          const isCur = current?.model === m.id
          return (
            <button
              type="button"
              key={m.id}
              role="radio"
              aria-checked={isSel}
              className={`model-row ${isSel ? 'selected' : ''} ${m.status}`}
              onClick={() => pick(m)}
              disabled={pickDisabled && !isSel}
            >
              <span className="radio">{isSel && <Check size={11} strokeWidth={3} />}</span>
              <span className="model-main">
                <span className="model-title">
                  <span className="model-name mono">{m.id}</span>
                  {m.recommended && <Badge tone="accent">recommended</Badge>}
                  <StatusBadge status={m.status} />
                  {isCur && <Badge tone="muted">current</Badge>}
                </span>
                <span className="model-best">{m.best_for}</span>
                {m.notes && <span className="model-notes">{m.notes}</span>}
                <span className="model-specs">
                  <Spec k="price" v={formatPrice(m.price_per_mtok)} />
                  <Spec k="free" v={m.free_mtok ? `${formatMTok(m.free_mtok)} tok` : 'none'} />
                  <Spec k="context" v={`${formatTokens(m.context_tokens)} tok`} />
                  <Spec k="dims" v={m.dims.join(' · ')} />
                </span>
              </span>
            </button>
          )
        })}
      </div>
      {hiddenCount > 0 && (
        <button type="button" className="link-btn" onClick={() => setShowOld(true)}>
          <ChevronDown size={13} /> Show {hiddenCount} deprecated model{hiddenCount === 1 ? '' : 's'}
        </button>
      )}

      {selected && (
        <div className="field dim-field">
          <label>
            Dimensions{' '}
            {lockedDim && <EnvLock settingKey="voyage_dim" compact />}
          </label>
          {selected.dims.length <= 1 ? (
            <div className="dim-fixed mono">{selected.default_dim} <span>fixed size for {selected.id}</span></div>
          ) : (
            <div className="pill-group dim-pills" role="radiogroup" aria-label="Dimensions">
              {selected.dims.map((d) => (
                <button
                  type="button"
                  key={d}
                  className={`pill ${d === dim ? 'active' : ''}`}
                  onClick={() => !disabled && !lockedDim && onChange(selected.id, d)}
                  disabled={(disabled || lockedDim) && d !== dim}
                >
                  {d}
                  {d === selected.default_dim && <small> default</small>}
                </button>
              ))}
            </div>
          )}
          <div className="field-hint">
            More dimensions keep a little more detail but use more storage; 1024 is the sweet spot for code. Vectors of different sizes can't share a collection.
          </div>
        </div>
      )}

      {compatNote && (
        <div className="info-line">
          <Info size={14} />
          <span>{compatNote}</span>
        </div>
      )}
    </div>
  )
}

// ---------------------------------------------------------------------------
// Reranker picker
// ---------------------------------------------------------------------------

interface RerankPickerProps {
  models: RerankInfo[]
  value: string // "" = off
  onChange: (id: string) => void
  current?: string
  locked?: boolean
  disabled?: boolean
}

export function RerankPicker({ models, value, onChange, current, locked, disabled }: RerankPickerProps) {
  const off = value === ''
  const block = disabled || locked
  return (
    <div className="model-picker">
      {locked && <div className="lock-line"><EnvLock settingKey="reranker_model" /></div>}
      <div className="model-list" role="radiogroup" aria-label="Reranker">
        {models.map((m) => {
          const isSel = m.id === value
          return (
            <button
              type="button"
              key={m.id}
              role="radio"
              aria-checked={isSel}
              className={`model-row ${isSel ? 'selected' : ''}`}
              onClick={() => !block && onChange(m.id)}
              disabled={block && !isSel}
            >
              <span className="radio">{isSel && <Check size={11} strokeWidth={3} />}</span>
              <span className="model-main">
                <span className="model-title">
                  <span className="model-name mono">{m.id}</span>
                  {m.recommended && <Badge tone="accent">recommended</Badge>}
                  <StatusBadge status={m.status} />
                  {current === m.id && <Badge tone="muted">current</Badge>}
                </span>
                <span className="model-best">{m.best_for}</span>
                <span className="model-specs">
                  <Spec k="price" v={formatPrice(m.price_per_mtok)} />
                  <Spec k="free" v={m.free_mtok ? `${formatMTok(m.free_mtok)} tok` : 'none'} />
                  <Spec k="context" v={`${formatTokens(m.context_tokens)} tok`} />
                  <Spec k="query" v={`${formatTokens(m.query_tokens)} tok`} />
                </span>
              </span>
            </button>
          )
        })}
        <button
          type="button"
          role="radio"
          aria-checked={off}
          className={`model-row ${off ? 'selected' : ''}`}
          onClick={() => !block && onChange('')}
          disabled={block && !off}
        >
          <span className="radio">{off && <Check size={11} strokeWidth={3} />}</span>
          <span className="model-main">
            <span className="model-title">
              <span className="model-name mono">Off</span>
              {current === '' && <Badge tone="muted">current</Badge>}
            </span>
            <span className="model-best">
              No reranking: results keep their vector (+ lexical) order. Cheapest, but top results are less precise.
            </span>
          </span>
        </button>
      </div>
      <div className="field-hint">
        The reranker re-scores the top candidates of every search. It only costs tokens at query time, so switching it never re-embeds anything.
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Re-embed impact
// ---------------------------------------------------------------------------

interface ImpactProps {
  models: ModelInfo[]
  current: { model: string; dim: number }
  next: { model: string; dim: number }
  /** Stored chunks (from /api/server store.rows). */
  rows: number
  /** Stored text characters (≈ chars/4 tokens). */
  contentChars: number
}

export type ImpactKind = 'none' | 'compatible' | 'reembed'

export function reembedImpact(models: ModelInfo[], current: { model: string; dim: number }, next: { model: string; dim: number }): ImpactKind {
  if (current.model === next.model && current.dim === next.dim) return 'none'
  const a = models.find((m) => m.id === current.model)
  const b = models.find((m) => m.id === next.model)
  if (a && b && a.family && a.family === b.family && current.dim === next.dim) return 'compatible'
  return 'reembed'
}

/** What changing the embedding model/dimension costs: chunks, tokens, dollars. */
export function ReembedImpact({ models, current, next, rows, contentChars }: ImpactProps) {
  const kind = reembedImpact(models, current, next)
  if (kind === 'none') return null
  const target = models.find((m) => m.id === next.model)
  const change = (
    <span className="mono">
      {current.model} · {current.dim} <ArrowRight size={11} style={{ verticalAlign: '-1px' }} /> {next.model} · {next.dim}
    </span>
  )
  if (kind === 'compatible') {
    return (
      <SuccessBox>
        <b>No re-embed needed.</b> {change} — both models are in the {target?.family} family and share one embedding space at the same dimension, so existing vectors stay valid.
      </SuccessBox>
    )
  }
  const tokens = estimateTokens(contentChars)
  const cost = estimateCost(tokens, target?.price_per_mtok || 0)
  return (
    <div className="impact">
      <WarnBox title="This change re-embeds your stored chunks.">
        {change}. Vectors from a different model or size can't be mixed, so after the restart the server re-embeds every chunk in the background (progress shows on the Overview). Until it finishes, chunks that are not re-embedded yet may be missing from search results.
      </WarnBox>
      <div className="impact-grid">
        <div><span className="ik">Chunks to re-embed</span><span className="iv mono">{formatNumber(rows)}</span></div>
        <div><span className="ik">Estimated tokens</span><span className="iv mono">≈ {formatTokens(tokens)}</span></div>
        <div>
          <span className="ik">Estimated cost</span>
          <span className="iv mono">{target ? `≈ ${formatUSD(cost)}` : 'unknown'}</span>
        </div>
      </div>
      <div className="field-hint">
        Estimate: {formatNumber(contentChars)} stored characters ÷ 4 ≈ {formatTokens(tokens)} tokens at {target ? formatPrice(target.price_per_mtok) : 'the model price'}.
        {target?.free_mtok
          ? ` Voyage includes ${formatMTok(target.free_mtok)} free tokens for ${target.id} per account — if you haven't used them, this may cost nothing.`
          : ''}
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Test result
// ---------------------------------------------------------------------------

export function ModelTestResult({ result, embedModel, rerankModel }: { result: ModelTestResponse; embedModel: string; rerankModel?: string }) {
  const e = result.embed
  return (
    <div className="model-test">
      <TestResultRow
        label="Embedding"
        detail={`${embedModel}${e.dim ? ` · ${e.dim}-dim` : ''}${e.tokens ? ` · ${e.tokens} tok` : ''}`}
        result={e}
      />
      {result.rerank ? (
        <TestResultRow label="Reranker" detail={rerankModel} result={result.rerank} />
      ) : (
        rerankModel === '' && <div className="field-hint"><ChevronRight size={11} /> Reranker is off — not tested.</div>
      )}
    </div>
  )
}
