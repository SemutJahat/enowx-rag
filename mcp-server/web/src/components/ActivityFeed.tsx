import type { SSEEvent } from '../lib/sse'
import { clockTime, fullTime, relativeTime } from '../lib/format'

type Tone = 'good' | 'crit' | 'warn' | 'accent' | 'dim'

interface Described {
  label: string
  desc: string
  tone: Tone
}

function str(v: unknown): string {
  return v === undefined || v === null ? '' : String(v)
}

function num(v: unknown): number {
  return typeof v === 'number' ? v : Number(v) || 0
}

/** Turn an event into a one-line human description. */
export function describeEvent(ev: SSEEvent): Described {
  const d = (ev.data || {}) as Record<string, unknown>
  const project = str(d.project_id || d.project)
  const p = project ? `${project}: ` : ''
  switch (ev.type) {
    case 'index_started':
      return { label: 'index started', desc: `${p}${str(d.directory)}`, tone: 'dim' }
    case 'index_completed':
      return {
        label: 'indexed',
        desc: `${p}${num(d.indexed)} new · ${num(d.updated)} updated · ${num(d.deleted)} deleted${d.files_scanned !== undefined ? ` · ${num(d.files_scanned)} files` : ''}`,
        tone: 'good',
      }
    case 'index_failed':
      return { label: 'index failed', desc: `${p}${str(d.error)}`, tone: 'crit' }
    case 'query_executed':
      return {
        label: 'query',
        desc: `${p}"${str(d.query).slice(0, 80)}"${d.candidates !== undefined ? ` · ${num(d.candidates)} candidates` : ''}`,
        tone: 'accent',
      }
    case 'project_created':
      return { label: 'project created', desc: project, tone: 'good' }
    case 'project_deleted':
      return { label: 'project deleted', desc: project, tone: 'warn' }
    case 'points_deleted':
      return { label: 'chunks deleted', desc: `${p}${num(d.count)} removed`, tone: 'warn' }
    case 'project_imported':
      return {
        label: 'imported',
        desc: `${p}${num(d.count)} chunks · ${num(d.reused)} vectors reused · ${num(d.reembedded)} re-embedded`,
        tone: 'good',
      }
    case 'documents_indexed':
      return { label: 'notes saved', desc: `${p}${num(d.count)} document${num(d.count) === 1 ? '' : 's'}`, tone: 'good' }
    case 'job_started':
      return { label: `${str(d.kind) || 'job'} started`, desc: `${p}${str(d.message)}`, tone: 'dim' }
    case 'job_progress':
      return { label: `${str(d.kind) || 'job'}`, desc: `${p}${str(d.message)}`, tone: 'dim' }
    case 'job_completed':
      return { label: `${str(d.kind) || 'job'} done`, desc: `${p}${str(d.message)}`, tone: 'good' }
    case 'job_failed':
      return { label: `${str(d.kind) || 'job'} failed`, desc: `${p}${str(d.error || d.message)}`, tone: 'crit' }
    case 'server_restarting':
      return { label: 'restarting', desc: str(d.reason || d.message) || 'server restart requested', tone: 'warn' }
    case 'sync_reported': {
      const host = str(d.host)
      const r = (d.result || d) as Record<string, unknown>
      const counts = r.indexed !== undefined || r.updated !== undefined
        ? ` · ${num(r.indexed)} new · ${num(r.updated)} updated · ${num(r.deleted)} deleted`
        : ''
      return { label: 'client sync', desc: `${p}${host ? `from ${host}` : ''}${counts}`, tone: 'good' }
    }
    default:
      return { label: ev.type.replace(/_/g, ' '), desc: p.replace(/: $/, ''), tone: 'dim' }
  }
}

/** Persistent activity feed (history from /api/activity + live events). */
export function ActivityFeed({ events, limit = 30, now }: { events: SSEEvent[]; limit?: number; now: number }) {
  const rows = events.filter((e) => e.type !== 'job_progress').slice(0, limit)
  if (rows.length === 0) {
    return (
      <div className="panel-empty">
        No activity yet. Indexing, client syncs, searches and jobs show up here as they happen.
      </div>
    )
  }
  return (
    <ul className="feed">
      {rows.map((ev, i) => {
        const d = describeEvent(ev)
        return (
          <li key={`${ev.type}-${ev.timestamp}-${i}`} className="feed-row">
            <span className={`feed-dot ${d.tone}`} />
            <span className="feed-label">{d.label}</span>
            <span className="feed-desc" title={d.desc}>{d.desc}</span>
            <span className="feed-time mono" title={`${fullTime(ev.timestamp)} (${clockTime(ev.timestamp)})`}>
              {relativeTime(ev.timestamp, now)}
            </span>
          </li>
        )
      })}
    </ul>
  )
}
