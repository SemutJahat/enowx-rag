// Re-index flow (POST /projects/{id}/reindex with no body → note + live job)
// and the project source list. Shared by Overview and Chunks.

import { useCallback, useState } from 'react'
import { Info, Server, X } from 'lucide-react'
import { api, errorText, type Job, type ProjectSource } from '../lib/api'
import { useJob, useJobs } from '../lib/sse'
import { formatNumber, fullTime, relativeTime } from '../lib/format'
import { JobProgress } from './JobProgress'
import { Badge, ErrorBox } from './ui'

export interface ReindexState {
  busy: boolean
  note?: string
  mode?: 'files' | 'reembed'
  job?: Job
  error?: string
}

export function useReindex() {
  const { trackJob } = useJobs()
  const [state, setState] = useState<Record<string, ReindexState>>({})

  const start = useCallback(async (projectId: string) => {
    setState((s) => ({ ...s, [projectId]: { busy: true } }))
    try {
      const r = await api.reindex(projectId)
      if (r.job) trackJob(r.job)
      setState((s) => ({ ...s, [projectId]: { busy: false, note: r.note, mode: r.mode, job: r.job } }))
    } catch (e) {
      setState((s) => ({ ...s, [projectId]: { busy: false, error: errorText(e, 'Re-index failed') } }))
    }
  }, [trackJob])

  const dismiss = useCallback((projectId: string) => {
    setState((s) => {
      const next = { ...s }
      delete next[projectId]
      return next
    })
  }, [])

  return { state, start, dismiss }
}

/** Note + live progress for a started re-index. */
export function ReindexStatus({ st, onDismiss }: { st: ReindexState; onDismiss: () => void }) {
  const live = useJob(st.job?.id)
  const job = live || st.job
  const finished = !st.busy && (!job || job.state !== 'running')
  if (st.busy) return null
  return (
    <div className="reindex-status">
      {finished && (
        <button className="icon-btn tiny dismiss" onClick={onDismiss} title="Dismiss">
          <X size={12} />
        </button>
      )}
      {st.error && <ErrorBox>{st.error}</ErrorBox>}
      {st.note && (
        <div className="info-line">
          <Info size={14} />
          <span>
            {st.mode && <Badge tone={st.mode === 'files' ? 'good' : 'accent'}>{st.mode === 'files' ? 'syncing files' : 're-embedding'}</Badge>}{' '}
            {st.note}
          </span>
        </div>
      )}
      {st.job && <JobProgress jobId={st.job.id} initial={st.job} compact />}
    </div>
  )
}

/** host:dir per source, with an on-server badge and last sync time. */
export function SourceList({ sources, now, detailed }: { sources: ProjectSource[]; now: number; detailed?: boolean }) {
  if (sources.length === 0) {
    return <span className="faint">no source recorded</span>
  }
  return (
    <div className="sources">
      {sources.map((s, i) => (
        <div className="source" key={`${s.host}-${s.dir}-${i}`}>
          <span className="source-path mono" title={`${s.host}:${s.dir}`}>
            <span className="source-host">{s.host || '?'}</span>:{s.dir}
          </span>
          {s.on_server && (
            <Badge tone="good" title="This folder exists on the server, so re-index can scan it directly.">
              <Server size={10} /> on server
            </Badge>
          )}
          <span className="source-sync faint" title={s.last_sync ? fullTime(s.last_sync) : ''}>
            {s.last_sync ? `synced ${relativeTime(s.last_sync, now)}` : 'never synced'}
          </span>
          {detailed && s.last_result && (
            <span className="source-result faint mono">
              last sync: {formatNumber(s.last_result.files_scanned)} files · {formatNumber(s.last_result.indexed)} new · {formatNumber(s.last_result.updated)} updated · {formatNumber(s.last_result.deleted)} deleted
              {s.last_result.skipped ? ` · ${formatNumber(s.last_result.skipped)} unchanged` : ''}
            </span>
          )}
        </div>
      ))}
    </div>
  )
}
