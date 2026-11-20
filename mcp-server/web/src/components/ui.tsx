// Small shared UI pieces: badges, env-lock notes, secret inputs, confirm
// dialogs, status rows.

import { useEffect, useState, type ReactNode } from 'react'
import { AlertTriangle, CheckCircle2, Eye, EyeOff, Key, Loader2, Lock, XCircle } from 'lucide-react'
import { envVarFor } from '../lib/api'

export type BadgeTone = 'good' | 'warn' | 'crit' | 'accent' | 'muted'

export function Badge({ tone = 'muted', children, title }: { tone?: BadgeTone; children: ReactNode; title?: string }) {
  return (
    <span className={`badge ${tone}`} title={title}>
      {children}
    </span>
  )
}

/** "🔒 set by environment variable RAG_X" for env-locked settings. */
export function EnvLock({ settingKey, compact }: { settingKey: string; compact?: boolean }) {
  const name = envVarFor(settingKey)
  return (
    <span className="env-lock" title={`Set by environment variable ${name}; change it where the server is started.`}>
      <Lock size={12} />
      {compact ? <span className="mono">{name}</span> : <>set by environment variable <span className="mono">{name}</span></>}
    </span>
  )
}

interface SecretInputProps {
  value: string
  onChange: (v: string) => void
  placeholder?: string
  disabled?: boolean
  autoFocus?: boolean
  onEnter?: () => void
  id?: string
}

/** Password-style input with a key icon and a reveal toggle. */
export function SecretInput({ value, onChange, placeholder, disabled, autoFocus, onEnter, id }: SecretInputProps) {
  const [show, setShow] = useState(false)
  return (
    <div className="input-wrapper">
      <Key size={15} strokeWidth={1.5} className="input-icon" />
      <input
        id={id}
        className="input mono with-icon with-reveal"
        type={show ? 'text' : 'password'}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && onEnter) onEnter()
        }}
        placeholder={placeholder}
        disabled={disabled}
        autoFocus={autoFocus}
        autoComplete="off"
        spellCheck={false}
      />
      <button type="button" className="reveal-btn" onClick={() => setShow(!show)} title={show ? 'Hide' : 'Reveal'}>
        {show ? <EyeOff size={16} /> : <Eye size={16} />}
      </button>
    </div>
  )
}

interface ConfirmDialogProps {
  title: string
  children: ReactNode
  confirmLabel: string
  danger?: boolean
  busy?: boolean
  error?: string
  onConfirm: () => void
  onCancel: () => void
}

/** Modal confirmation dialog (Escape cancels). */
export function ConfirmDialog({ title, children, confirmLabel, danger, busy, error, onConfirm, onCancel }: ConfirmDialogProps) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !busy) onCancel()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [busy, onCancel])
  return (
    <div className="modal-backdrop" onClick={() => !busy && onCancel()}>
      <div className="modal" role="dialog" aria-modal="true" aria-label={title} onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <AlertTriangle size={18} className={danger ? 'crit-icon' : 'warn-icon'} />
          <h3>{title}</h3>
        </div>
        <div className="modal-body">{children}</div>
        {error && <div className="test-error" style={{ margin: '0 18px 12px' }}><XCircle size={15} /><span>{error}</span></div>}
        <div className="modal-actions">
          <button className="btn" onClick={onCancel} disabled={busy}>Cancel</button>
          <button className={`btn ${danger ? 'danger' : 'primary'}`} onClick={onConfirm} disabled={busy}>
            {busy && <Loader2 size={14} className="spin" />}
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  )
}

/** One pass/fail line for a connectivity test. */
export function TestResultRow({ label, detail, result }: {
  label: string
  detail?: string
  result: { ok: boolean; message: string; latency_ms?: number } | null | undefined
}) {
  if (!result) return null
  return (
    <div className={`test-result ${result.ok ? '' : 'fail'}`}>
      {result.ok ? <CheckCircle2 size={16} className="good-icon" /> : <XCircle size={16} className="crit-icon" />}
      <div className="test-info">
        <div className="comp-name">
          {label} {detail && <span className="mono">— {detail}</span>}
        </div>
        <div className="test-msg">{result.message}</div>
      </div>
      <span className={`latency ${result.ok ? 'ok' : ''}`}>
        {result.ok && result.latency_ms !== undefined ? `${Math.round(result.latency_ms)} ms` : '—'}
      </span>
    </div>
  )
}

export function ErrorBox({ children }: { children: ReactNode }) {
  return (
    <div className="test-error">
      <XCircle size={16} style={{ flex: 'none' }} />
      <span>{children}</span>
    </div>
  )
}

export function WarnBox({ title, children }: { title?: ReactNode; children?: ReactNode }) {
  return (
    <div className="warn-box">
      <AlertTriangle size={16} className="warn-icon" />
      <div className="warn-text">
        {title && <b>{title} </b>}
        {children}
      </div>
    </div>
  )
}

export function SuccessBox({ children }: { children: ReactNode }) {
  return (
    <div className="success-box">
      <CheckCircle2 size={16} style={{ flex: 'none' }} />
      <span>{children}</span>
    </div>
  )
}

export function Spinner({ label }: { label?: string }) {
  return (
    <div className="inline-loading">
      <Loader2 size={15} className="spin" />
      {label || 'Loading…'}
    </div>
  )
}
