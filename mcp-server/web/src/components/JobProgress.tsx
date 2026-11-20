import { CheckCircle2, Loader2, XCircle } from 'lucide-react'
import type { Job } from '../lib/api'
import { useJob } from '../lib/sse'
import { formatDuration, formatNumber, relativeTime } from '../lib/format'

const KIND_LABEL: Record<string, string> = {
  reindex: 'Re-index',
  reembed: 'Re-embed',
  import: 'Import',
  refresh: 'Refresh',
}

function jobPercent(job: Job): number | null {
  if (!job.total) return job.state === 'done' ? 100 : null
  return Math.max(0, Math.min(100, Math.round((job.done / job.total) * 100)))
}

function elapsedSeconds(job: Job): number {
  const start = new Date(job.started_at).getTime()
  const end = job.finished_at ? new Date(job.finished_at).getTime() : Date.now()
  return (end - start) / 1000
}

interface JobProgressProps {
  /** A job object (e.g. from a list) … */
  job?: Job
  /** … or an id to follow live through the events context. */
  jobId?: string
  /** Fallback while the id is not yet known to the context. */
  initial?: Job
  compact?: boolean
}

/** A job card: kind, project, message, progress bar, elapsed time, result/error. */
export function JobProgress({ job: jobProp, jobId, initial, compact }: JobProgressProps) {
  const live = useJob(jobId)
  const job = jobProp || live || initial
  if (!job) return null
  const pct = jobPercent(job)
  const state = job.state
  return (
    <div className={`job ${state} ${compact ? 'compact' : ''}`}>
      <div className="job-head">
        {state === 'running' && <Loader2 size={14} className="spin accent-icon" />}
        {state === 'done' && <CheckCircle2 size={14} className="good-icon" />}
        {state === 'failed' && <XCircle size={14} className="crit-icon" />}
        <span className="job-kind">{KIND_LABEL[job.kind] || job.kind}</span>
        {job.project && <span className="job-proj mono">{job.project}</span>}
        <span className="job-meta mono">
          {state === 'running'
            ? formatDuration(elapsedSeconds(job))
            : job.finished_at
              ? relativeTime(job.finished_at)
              : ''}
        </span>
      </div>
      {job.message && <div className="job-msg">{job.message}</div>}
      {state === 'running' || pct !== null ? (
        <div className="job-bar-row">
          <div className={`job-bar ${pct === null ? 'indeterminate' : ''}`}>
            <i style={{ width: pct === null ? undefined : `${pct}%` }} />
          </div>
          <span className="job-count mono">
            {job.total ? `${formatNumber(job.done)} / ${formatNumber(job.total)}` : ''}
            {pct !== null ? ` · ${pct}%` : ''}
          </span>
        </div>
      ) : null}
      {state === 'failed' && job.error && <div className="job-error">{job.error}</div>}
    </div>
  )
}
