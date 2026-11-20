import { useCallback, useEffect, useRef, useState } from 'react'
import { Database, Cpu, ListOrdered, ShieldCheck, ShieldAlert, Server as ServerIcon, RefreshCw, Plug, Upload, Inbox } from 'lucide-react'
import type { Page } from '../App'
import { api, errorText, type MetricsResponse, type ProjectSummary, type ServerInfo } from '../lib/api'
import { useEventListener, useEvents, useJobs } from '../lib/sse'
import { useTick } from '../lib/server'
import { formatBytes, formatDuration, formatMs, formatNumber, formatPrice, formatTokens, fullTime, relativeTime, formatVersion } from '../lib/format'
import { ActivityFeed } from '../components/ActivityFeed'
import { JobProgress } from '../components/JobProgress'
import { ProjectsTable } from '../components/ProjectsTable'
import { TimelineChart } from '../components/TimelineChart'
import { Badge, ErrorBox, Spinner, WarnBox } from '../components/ui'

interface OverviewProps {
  projects: ProjectSummary[] | null
  onRefreshProjects: () => void
  onNavigate: (p: Page) => void
  onOpenProject: (id: string) => void
}

const STORE_LABEL: Record<string, string> = {
  pgvector: 'PostgreSQL + pgvector',
  sqlite: 'SQLite (built-in)',
  qdrant: 'Qdrant',
  chroma: 'Chroma',
  remote: 'Remote server',
}

const SERVER_EVENTS = ['job_completed', 'job_failed', 'index_completed', 'project_deleted', 'documents_indexed', 'points_deleted', 'sync_reported']

export function Overview({ projects, onRefreshProjects, onNavigate, onOpenProject }: OverviewProps) {
  const [server, setServer] = useState<ServerInfo | null>(null)
  const [serverError, setServerError] = useState('')
  const [metrics, setMetrics] = useState<MetricsResponse | null>(null)
  const { events } = useEvents(100)
  const { jobs } = useJobs()
  const now = useTick(20000)

  const loadServer = useCallback(() => {
    api.server().then((s) => {
      setServer(s)
      setServerError('')
    }).catch((e) => setServerError(errorText(e, 'Could not load server status')))
  }, [])

  const loadMetrics = useCallback(() => {
    api.metrics().then(setMetrics).catch(() => {})
  }, [])

  const refreshAll = useCallback(() => {
    loadServer()
    loadMetrics()
    onRefreshProjects()
  }, [loadServer, loadMetrics, onRefreshProjects])

  useEffect(() => {
    loadServer()
    loadMetrics()
    const t = window.setInterval(() => {
      loadServer()
      loadMetrics()
    }, 15000)
    return () => window.clearInterval(t)
  }, [loadServer, loadMetrics])

  const qTimer = useRef<number | undefined>(undefined)
  useEventListener(() => {
    window.clearTimeout(qTimer.current)
    qTimer.current = window.setTimeout(loadMetrics, 800)
  }, ['query_executed'])

  const sTimer = useRef<number | undefined>(undefined)
  useEventListener(() => {
    window.clearTimeout(sTimer.current)
    sTimer.current = window.setTimeout(loadServer, 800)
  }, SERVER_EVENTS)

  const running = jobs.filter((j) => j.state === 'running')
  const recentJobs = jobs.filter((j) => j.state !== 'running').slice(0, 5)
  const reembedJob = running.find((j) => j.kind === 'reembed')
  const totalFiles = (projects || []).reduce((s, p) => s + (p.file_count || 0), 0)
  const chunks = server?.store.rows ?? (projects || []).reduce((s, p) => s + p.chunk_count, 0)

  return (
    <>
      <div className="page-head">
        <h1>Overview</h1>
        <span className="id mono">{server ? `${server.hostname} · ${formatVersion(server.version)}` : ''}</span>
        <div className="head-actions">
          <button className="btn" onClick={refreshAll} title="Reload status">
            <RefreshCw size={14} strokeWidth={1.7} /> Refresh
          </button>
        </div>
      </div>

      {serverError && <ErrorBox>{serverError}</ErrorBox>}

      {server && server.stale_points > 0 && (
        <div className="banner">
          <WarnBox title={`${formatNumber(server.stale_points)} chunks are being re-embedded for ${server.embedder.model}.`}>
            They were stored with a different model or dimension. Search still works; results for those chunks improve as they finish.
          </WarnBox>
          {reembedJob && <JobProgress job={reembedJob} compact />}
        </div>
      )}

      {/* System status */}
      {server ? <SystemCards server={server} /> : !serverError && <Spinner label="Loading server status…" />}

      {/* KPIs */}
      <div className="kpis kpis-5">
        <div className="kpi">
          <div className="label">Projects</div>
          <div className="val tnum">{projects ? formatNumber(projects.length) : '—'}</div>
          <div className="sub">{projects ? `${formatNumber(totalFiles)} files tracked` : ''}</div>
        </div>
        <div className="kpi">
          <div className="label">Chunks</div>
          <div className="val tnum">{formatNumber(chunks)}</div>
          <div className="sub">{server?.store.size_bytes !== undefined ? `${formatBytes(server.store.size_bytes)} stored` : server?.embedder.model || ''}</div>
        </div>
        <div className="kpi">
          <div className="label">Queries · 24h</div>
          <div className="val tnum">{metrics ? formatNumber(metrics.queries_24h) : '—'}</div>
          <div className="sub">{metrics ? `${formatNumber(metrics.query_count)} total` : ''}</div>
        </div>
        <div className="kpi">
          <div className="label">Latency p50 / p95</div>
          {metrics && metrics.query_count > 0 ? (
            <>
              <div className="val tnum">{Math.round(metrics.p50_latency_ms)}<small> / {Math.round(metrics.p95_latency_ms)} ms</small></div>
              <div className="sub">avg {formatMs(metrics.avg_latency_ms)}</div>
            </>
          ) : (
            <><div className="val tnum">—</div><div className="sub">no queries yet</div></>
          )}
        </div>
        <div className="kpi">
          <div className="label">Tokens since start</div>
          {metrics && metrics.tokens_total > 0 ? (
            <>
              <div className="val tnum">{formatTokens(metrics.tokens_total)}</div>
              <div className="sub mono">{formatTokens(metrics.tokens_embed)} embed · {formatTokens(metrics.tokens_rerank)} rerank</div>
            </>
          ) : (
            <><div className="val tnum">0</div><div className="sub">{server ? `up ${formatDuration(server.uptime_s)}` : ''}</div></>
          )}
        </div>
      </div>

      <div className="grid-2-1">
        <section className="panel">
          <div className="panel-head">
            <h2>Queries · last 24 hours</h2>
            <span className="hint">{metrics ? `${formatNumber(metrics.queries_24h)} queries` : ''}</span>
          </div>
          <div className="panel-body">
            {metrics ? <TimelineChart buckets={metrics.timeline_24h || []} /> : <Spinner />}
          </div>
        </section>
        <BreakdownPanel metrics={metrics} />
      </div>

      {/* Projects */}
      <section className="panel">
        <div className="panel-head">
          <h2>Projects</h2>
          <span className="hint">{projects ? `${projects.length} on this server` : ''}</span>
        </div>
        <div className="panel-body flush">
          {projects === null ? (
            <div className="pad"><Spinner /></div>
          ) : projects.length === 0 ? (
            <div className="empty-state">
              <Inbox size={28} />
              <div><b>No projects yet.</b> Connect a machine — its agent indexes a project the first time it searches it.</div>
              <div className="empty-actions">
                <button className="btn primary" onClick={() => onNavigate('connect')}><Plug size={14} /> Connect a machine</button>
                <button className="btn" onClick={() => onNavigate('import')}><Upload size={14} /> Import a rag.db</button>
              </div>
            </div>
          ) : (
            <ProjectsTable projects={projects} now={now} onOpenProject={onOpenProject} onChanged={refreshAll} />
          )}
        </div>
      </section>

      <div className="grid-2">
        {/* Jobs */}
        <section className="panel">
          <div className="panel-head">
            <h2>Jobs</h2>
            <span className="hint">{running.length ? `${running.length} running` : 'idle'}</span>
          </div>
          <div className="panel-body">
            {running.length === 0 && recentJobs.length === 0 ? (
              <div className="panel-empty">
                No jobs yet. Re-index, re-embed and import runs appear here with live progress.
              </div>
            ) : (
              <div className="job-list">
                {running.map((j) => <JobProgress key={j.id} job={j} />)}
                {recentJobs.map((j) => <JobProgress key={j.id} job={j} compact />)}
              </div>
            )}
          </div>
        </section>

        {/* Activity */}
        <section className="panel">
          <div className="panel-head">
            <h2>Activity</h2>
            <span className="hint">live · persisted</span>
          </div>
          <div className="panel-body scroll-body">
            <ActivityFeed events={events} now={now} />
          </div>
        </section>
      </div>

      {/* Recent queries */}
      <section className="panel">
        <div className="panel-head">
          <h2>Recent queries</h2>
          <span className="hint">last 20</span>
        </div>
        <div className="panel-body flush">
          {!metrics ? (
            <div className="pad"><Spinner /></div>
          ) : (metrics.recent_queries || []).length === 0 ? (
            <div className="panel-empty pad">
              No searches yet. They appear here when an agent calls <span className="mono">rag_retrieve_context</span> or <span className="mono">rag_semantic_search</span>.
            </div>
          ) : (
            <div className="table-wrap">
            <table className="dtable">
              <thead>
                <tr>
                  <th>When</th>
                  <th>Project</th>
                  <th>Query</th>
                  <th className="num">Results</th>
                  <th className="num">Latency</th>
                  <th>Mode</th>
                  <th>Client</th>
                </tr>
              </thead>
              <tbody>
                {metrics.recent_queries.map((q, i) => (
                  <tr key={`${q.ts}-${i}`}>
                    <td data-label="When" className="faint nowrap" title={fullTime(q.ts)}>{relativeTime(q.ts, now)}</td>
                    <td data-label="Project" className="mono">{q.project}</td>
                    <td data-label="Query" className="query-cell" title={q.query}>{q.query}</td>
                    <td data-label="Results" className="num mono">{q.results}</td>
                    <td data-label="Latency" className="num mono">{formatMs(q.latency_ms)}</td>
                    <td data-label="Mode">
                      <span className="chips">
                        {q.hybrid && <Badge tone="muted">hybrid</Badge>}
                        {q.reranked && <Badge tone="accent">reranked</Badge>}
                        {!q.hybrid && !q.reranked && <Badge tone="muted">dense</Badge>}
                      </span>
                    </td>
                    <td data-label="Client" className="faint mono">{q.client || '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            </div>
          )}
        </div>
      </section>
    </>
  )
}

// ---------------------------------------------------------------------------

function Row({ k, v, title }: { k: string; v: React.ReactNode; title?: string }) {
  return (
    <div className="sys-row" title={title}>
      <span className="k">{k}</span>
      <span className="v">{v}</span>
    </div>
  )
}

function SystemCards({ server }: { server: ServerInfo }) {
  const s = server.store
  const e = server.embedder
  const r = server.reranker
  return (
    <div className="sys-grid">
      <div className={`sys-card ${s.ok ? '' : 'bad'}`}>
        <div className="sys-head">
          <Database size={15} />
          <span>Database</span>
          <span className={`sys-state ${s.ok ? 'good' : 'crit'}`}>{s.ok ? 'ok' : 'error'}</span>
        </div>
        <div className="sys-title">{STORE_LABEL[s.type] || s.type}</div>
        <div className="sys-target mono" title={s.target}>{s.target || '—'}</div>
        {s.error && <div className="sys-error">{s.error}</div>}
        <div className="sys-rows">
          {s.server_version && <Row k="server" v={<span className="mono">{s.server_version}</span>} />}
          {s.extension_version && <Row k="pgvector" v={<span className="mono">{s.extension_version}</span>} />}
          {s.size_bytes !== undefined && <Row k="size" v={<span className="mono">{formatBytes(s.size_bytes)}</span>} />}
          {s.rows !== undefined && <Row k="rows" v={<span className="mono">{formatNumber(s.rows)}</span>} />}
          {s.latency_ms !== undefined && <Row k="latency" v={<span className="mono">{formatMs(s.latency_ms)}</span>} />}
          {s.table && <Row k="table" v={<span className="mono">{s.table}{s.dim ? ` · ${s.dim}d` : ''}</span>} />}
        </div>
      </div>

      <div className={`sys-card ${e.key_set || e.type !== 'voyage' ? '' : 'bad'}`}>
        <div className="sys-head">
          <Cpu size={15} />
          <span>Embedder</span>
          <span className={`sys-state ${e.key_set || e.type !== 'voyage' ? 'good' : 'crit'}`}>
            {e.type === 'voyage' && !e.key_set ? 'no key' : e.type}
          </span>
        </div>
        <div className="sys-title mono">{e.model || '—'}</div>
        <div className="sys-target">{e.info?.best_for || `${e.dim}-dimensional vectors`}</div>
        <div className="sys-rows">
          <Row k="dimensions" v={<span className="mono">{e.dim}</span>} />
          {e.info && <Row k="price" v={<span className="mono">{formatPrice(e.info.price_per_mtok)}</span>} />}
          {e.info && <Row k="context" v={<span className="mono">{formatTokens(e.info.context_tokens)} tok</span>} />}
          {e.type === 'voyage' && <Row k="API key" v={e.key_set ? 'set' : <span className="crit-text">missing</span>} />}
        </div>
      </div>

      <div className="sys-card">
        <div className="sys-head">
          <ListOrdered size={15} />
          <span>Reranker</span>
          <span className={`sys-state ${r.enabled ? 'good' : 'muted'}`}>{r.enabled ? 'on' : 'off'}</span>
        </div>
        <div className="sys-title mono">{r.enabled ? r.model : 'Off'}</div>
        <div className="sys-target">{r.enabled ? r.info?.best_for || 'Re-scores the top search candidates.' : r.reason || 'Results keep their vector order.'}</div>
        {r.enabled && r.info && (
          <div className="sys-rows">
            <Row k="price" v={<span className="mono">{formatPrice(r.info.price_per_mtok)}</span>} />
            <Row k="context" v={<span className="mono">{formatTokens(r.info.context_tokens)} tok</span>} />
          </div>
        )}
      </div>

      <div className={`sys-card ${server.auth_required ? '' : 'warn'}`}>
        <div className="sys-head">
          {server.auth_required ? <ShieldCheck size={15} /> : <ShieldAlert size={15} />}
          <span>Access</span>
          <span className={`sys-state ${server.auth_required ? 'good' : 'warn'}`}>{server.auth_required ? 'token' : 'open'}</span>
        </div>
        <div className="sys-title">{server.auth_required ? 'Admin token required' : 'No admin token'}</div>
        <div className="sys-target">
          {server.auth_required
            ? 'API, MCP endpoint and dashboard need the bearer token.'
            : 'Anyone who can reach this server can use it. Set a token in Settings.'}
        </div>
        <div className="sys-rows">
          <Row k="auto-sync" v={server.auto_sync.enabled ? <span className="mono">every {server.auto_sync.interval}</span> : 'off'} />
          <Row k="jobs running" v={<span className="mono">{server.jobs_running}</span>} />
        </div>
      </div>

      <div className="sys-card">
        <div className="sys-head">
          <ServerIcon size={15} />
          <span>Server</span>
          <span className="sys-state good">up</span>
        </div>
        <div className="sys-title">up {formatDuration(server.uptime_s)}</div>
        <div className="sys-target mono" title={`started ${fullTime(server.started_at)}`}>{formatVersion(server.version)} · {server.hostname}</div>
        <div className="sys-rows">
          <Row k="os" v={<span className="mono">{server.os}</span>} />
          <Row k="go" v={<span className="mono">{server.go_version}</span>} />
        </div>
      </div>
    </div>
  )
}

function BreakdownPanel({ metrics }: { metrics: MetricsResponse | null }) {
  const comp = metrics?.last_query
  const total = comp ? comp.dense_count + comp.lexical_count : 0
  return (
    <section className="panel">
      <div className="panel-head">
        <h2>Retrieval breakdown</h2>
        <span className="hint mono">last query</span>
      </div>
      <div className="panel-body">
        {comp && total > 0 ? (
          <>
            <div className="compo">
              <i style={{ width: `${(comp.dense_count / total) * 100}%`, background: 'var(--accent)' }} />
              <i style={{ width: `${(comp.lexical_count / total) * 100}%`, background: 'var(--good)' }} />
            </div>
            <div className="compo-legend">
              <div className="lrow"><span className="sw" style={{ background: 'var(--accent)' }} />Dense (vector)<span className="lval">{comp.dense_count}</span></div>
              <div className="lrow"><span className="sw" style={{ background: 'var(--good)' }} />Lexical (full-text)<span className="lval">{comp.lexical_count}</span></div>
              <div className="lrow"><span className="sw" style={{ background: 'var(--border-strong)' }} />Candidates → results<span className="lval">{comp.candidates} → {comp.results}</span></div>
              {comp.reranked && (
                <div className="lrow"><span className="sw" style={{ background: 'var(--warn)' }} />Reranker moved<span className="lval">{comp.rerank_moved}</span></div>
              )}
            </div>
          </>
        ) : (
          <div className="panel-empty">
            {comp
              ? 'Dense-only retrieval for the last query — hybrid search on pgvector shows a dense/lexical split here.'
              : 'No query yet. The mix of vector and full-text hits for the latest search shows up here.'}
          </div>
        )}
      </div>
    </section>
  )
}
