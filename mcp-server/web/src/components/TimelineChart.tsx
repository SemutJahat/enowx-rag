import type { TimelineBucket } from '../lib/api'
import { formatMs, formatNumber } from '../lib/format'

function hourLabel(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return d.toLocaleTimeString('en-US', { hour: '2-digit', minute: '2-digit', hour12: false })
}

/** 24 hourly query buckets as a pure-CSS bar chart. */
export function TimelineChart({ buckets }: { buckets: TimelineBucket[] }) {
  const max = Math.max(1, ...buckets.map((b) => b.count))
  const total = buckets.reduce((s, b) => s + b.count, 0)
  if (buckets.length === 0) return <div className="panel-empty">No timeline data yet.</div>
  return (
    <div className="tl">
      <div className="tl-plot">
        <div className="tl-axis mono" aria-hidden="true">
          <span>{formatNumber(max)}</span>
          <span>0</span>
        </div>
        <div className="tl-bars" role="img" aria-label={`${total} queries in the last 24 hours`}>
          {buckets.map((b, i) => {
            const h = b.count === 0 ? 0 : Math.max(4, (b.count / max) * 100)
            return (
              <div
                key={b.t || i}
                className="tl-col"
                title={`${hourLabel(b.t)} — ${formatNumber(b.count)} quer${b.count === 1 ? 'y' : 'ies'}${b.count ? ` · avg ${formatMs(b.avg_latency_ms)}` : ''}`}
              >
                <i className={b.count === 0 ? 'zero' : ''} style={{ height: `${h}%` }} />
              </div>
            )
          })}
        </div>
      </div>
      <div className="tl-x mono" aria-hidden="true">
        {buckets.map((b, i) => (
          <span key={b.t || i}>{i % 6 === 0 ? hourLabel(b.t) : ''}</span>
        ))}
      </div>
    </div>
  )
}
