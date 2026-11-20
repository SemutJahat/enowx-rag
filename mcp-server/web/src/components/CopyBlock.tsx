import { useState } from 'react'
import { Check, Copy, Eye, EyeOff } from 'lucide-react'

/** Copy text to the clipboard, falling back to a hidden textarea on http:// origins. */
export async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // fall through
  }
  try {
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    const ok = document.execCommand('copy')
    document.body.removeChild(ta)
    return ok
  } catch {
    return false
  }
}

export function maskSecret(secret: string): string {
  if (!secret) return ''
  if (secret.length <= 8) return '•'.repeat(secret.length)
  return secret.slice(0, 4) + '•'.repeat(Math.min(16, secret.length - 8)) + secret.slice(-4)
}

export function CopyButton({ text, label = 'copy' }: { text: string; label?: string }) {
  const [state, setState] = useState<'idle' | 'ok' | 'fail'>('idle')
  const onClick = async () => {
    const ok = await copyText(text)
    setState(ok ? 'ok' : 'fail')
    window.setTimeout(() => setState('idle'), 1800)
  }
  return (
    <button type="button" className="copy-btn" onClick={onClick} title="Copy to clipboard">
      {state === 'ok' ? <Check size={12} /> : <Copy size={12} />}
      {state === 'ok' ? 'copied' : state === 'fail' ? 'press ⌘C' : label}
    </button>
  )
}

interface CopyBlockProps {
  title: string
  code: string
  /** When set, occurrences of this secret are masked until revealed (copy always copies the real value). */
  secret?: string
  /** Controlled reveal state; uncontrolled when omitted. */
  revealed?: boolean
  onToggleReveal?: () => void
  className?: string
}

/** A titled code block with a copy button (and optional secret masking). */
export function CopyBlock({ title, code, secret, revealed, onToggleReveal, className }: CopyBlockProps) {
  const [localReveal, setLocalReveal] = useState(false)
  const isRevealed = revealed ?? localReveal
  const toggle = onToggleReveal ?? (() => setLocalReveal((v) => !v))
  const display = secret && !isRevealed ? code.split(secret).join(maskSecret(secret)) : code
  return (
    <div className={`code-block ${className || ''}`}>
      <div className="code-head">
        <span className="fname mono">{title}</span>
        {secret && (
          <button type="button" className="copy-btn reveal" onClick={toggle} title={isRevealed ? 'Hide token' : 'Show token'}>
            {isRevealed ? <EyeOff size={12} /> : <Eye size={12} />}
            {isRevealed ? 'hide' : 'show'}
          </button>
        )}
        <CopyButton text={code} />
      </div>
      <pre className="code-body mono wrap">{display}</pre>
    </div>
  )
}
