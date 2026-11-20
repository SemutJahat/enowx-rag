// Formatting helpers shared by every page: numbers, bytes, tokens, money,
// durations and relative times.

/** 12345 → "12,345". */
export function formatNumber(n: number | undefined | null): string {
  if (n === undefined || n === null || Number.isNaN(n)) return '—'
  return Math.round(n).toLocaleString('en-US')
}

/** 1234 → "1.2k", 3_400_000 → "3.4M". */
export function formatCompact(n: number | undefined | null): string {
  if (n === undefined || n === null || Number.isNaN(n)) return '—'
  const abs = Math.abs(n)
  if (abs >= 1e9) return trim(n / 1e9) + 'B'
  if (abs >= 1e6) return trim(n / 1e6) + 'M'
  if (abs >= 1e3) return trim(n / 1e3) + 'k'
  return String(Math.round(n))
}

function trim(v: number): string {
  return (Math.abs(v) >= 100 ? v.toFixed(0) : v.toFixed(1)).replace(/\.0$/, '')
}

/** Token counts read best compact ("53.9M"). */
export const formatTokens = formatCompact

/** Millions of tokens as given by the model catalog: 200 → "200M", 0.5 → "500k". */
export function formatMTok(m: number): string {
  if (!m) return '—'
  return formatCompact(m * 1e6)
}

/** 1536 → "1.5 KB". */
export function formatBytes(n: number | undefined | null): string {
  if (n === undefined || n === null || Number.isNaN(n)) return '—'
  if (n < 1024) return `${n} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v >= 100 ? v.toFixed(0) : v.toFixed(1)} ${units[i]}`
}

/** US dollars: 0 → "$0", 0.004 → "<$0.01", 12.3 → "$12.30". */
export function formatUSD(n: number): string {
  if (!n || n <= 0) return '$0'
  if (n < 0.01) return '<$0.01'
  return '$' + n.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

/** Price per 1M tokens: 0.06 → "$0.06 / 1M". */
export function formatPrice(perMTok: number): string {
  if (!perMTok) return 'free'
  return `$${perMTok < 0.1 ? perMTok.toFixed(3).replace(/0$/, '') : perMTok.toFixed(2)} / 1M`
}

/** Milliseconds: 4.2 → "4 ms", 1530 → "1.5 s". */
export function formatMs(ms: number | undefined | null): string {
  if (ms === undefined || ms === null || Number.isNaN(ms)) return '—'
  if (ms >= 1000) return `${(ms / 1000).toFixed(1)} s`
  return `${Math.round(ms)} ms`
}

/** Seconds → "3d 4h", "2h 5m", "12m", "40s". */
export function formatDuration(seconds: number | undefined | null): string {
  if (seconds === undefined || seconds === null || Number.isNaN(seconds)) return '—'
  const s = Math.max(0, Math.floor(seconds))
  const d = Math.floor(s / 86400)
  const h = Math.floor((s % 86400) / 3600)
  const m = Math.floor((s % 3600) / 60)
  if (d > 0) return `${d}d ${h}h`
  if (h > 0) return `${h}h ${m}m`
  if (m > 0) return `${m}m`
  return `${s}s`
}

/** ISO time → "just now", "2m ago", "3h ago", "5d ago" (or "in 2m"). */
export function relativeTime(iso: string | undefined | null, now: number = Date.now()): string {
  if (!iso) return '—'
  const t = new Date(iso).getTime()
  if (Number.isNaN(t)) return '—'
  const diff = Math.round((now - t) / 1000)
  const future = diff < 0
  const a = Math.abs(diff)
  let out: string
  if (a < 10) return 'just now'
  if (a < 60) out = `${a}s`
  else if (a < 3600) out = `${Math.floor(a / 60)}m`
  else if (a < 86400) out = `${Math.floor(a / 3600)}h`
  else if (a < 86400 * 30) out = `${Math.floor(a / 86400)}d`
  else return new Date(t).toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' })
  return future ? `in ${out}` : `${out} ago`
}

/** ISO time → "14:05:09" (local, 24h). */
export function clockTime(iso: string | undefined | null): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '—'
  return d.toLocaleTimeString('en-US', { hour12: false })
}

/** ISO time → full local date-time for tooltips. */
export function fullTime(iso: string | undefined | null): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return d.toLocaleString('en-US', { hour12: false })
}

/** Rough token estimate for stored text (≈ 4 chars per token). */
export function estimateTokens(chars: number): number {
  return Math.ceil((chars || 0) / 4)
}

/** Cost in USD for a token count at a per-1M-token price. */
export function estimateCost(tokens: number, perMTok: number): number {
  return (tokens / 1e6) * (perMTok || 0)
}

/** "1 chunk" / "3 chunks". */
export function plural(n: number, word: string, pluralWord?: string): string {
  return `${formatNumber(n)} ${n === 1 ? word : pluralWord || word + 's'}`
}

/** "0.4.1" / "v0.4.1" → "v0.4.1"; "dev" stays "dev". */
export function formatVersion(v: string | undefined | null): string {
  if (!v) return ''
  return /^\d/.test(v) ? `v${v}` : v
}
