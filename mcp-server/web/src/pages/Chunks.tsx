import { useState, useEffect, useCallback } from 'react'
import { Inbox, Trash2, ChevronLeft, ChevronRight, Filter, RefreshCw, Loader2, Plug } from 'lucide-react'
import type { Page } from '../App'
import { api, errorText, type PointInfo, type ProjectSummary } from '../lib/api'
import { useEventListener } from '../lib/sse'
import { useTick } from '../lib/server'
import { formatNumber } from '../lib/format'
import { ReindexStatus, SourceList, useReindex } from '../components/Reindex'
import { ErrorBox, Spinner } from '../components/ui'

interface ChunksProps {
  projects: ProjectSummary[] | null
  activeProject: string
  onSelectProject: (id: string) => void
  onNavigate: (p: Page) => void
}

const LIMIT = 20

export function Chunks({ projects, activeProject, onSelectProject, onNavigate }: ChunksProps) {
  const [points, setPoints] = useState<PointInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [filter, setFilter] = useState('')
  const [appliedFilter, setAppliedFilter] = useState('')
  const [offset, setOffset] = useState(0)
  const [deletingId, setDeletingId] = useState<string | null>(null)
  const reindex = useReindex()
  const now = useTick(30000)

  const project = projects?.find((p) => p.project_id === activeProject)

  const fetchPoints = useCallback(async () => {
    if (!activeProject) {
      setPoints([])
      setLoading(false)
      return
    }
    setLoading(true)
    setError('')
    try {
      const data = await api.listPoints(activeProject, { source_file: appliedFilter || undefined, offset, limit: LIMIT })
      setPoints(Array.isArray(data) ? data : [])
    } catch (e) {
      setPoints([])
      setError(errorText(e, 'Failed to load chunks'))
    } finally {
      setLoading(false)
    }
  }, [activeProject, appliedFilter, offset])

  useEffect(() => {
    fetchPoints()
  }, [fetchPoints])

  // Reset paging when switching projects.
  useEffect(() => {
    setOffset(0)
    setFilter('')
    setAppliedFilter('')
  }, [activeProject])

  // Refresh when this project's chunks change.
  useEventListener((ev) => {
    const d = ev.data as Record<string, unknown> | undefined
    const pid = d?.project_id ?? d?.project
    if (pid === activeProject) fetchPoints()
  }, ['index_completed', 'documents_indexed', 'job_completed', 'sync_reported'])

  const handleDelete = useCallback(async (pointId: string) => {
    if (!activeProject) return
    setDeletingId(pointId)
    try {
      await api.deletePoint(activeProject, pointId)
      setPoints((prev) => prev.filter((p) => p.id !== pointId))
    } catch (e) {
      setError(errorText(e, 'Delete failed'))
      fetchPoints()
    } finally {
      setDeletingId(null)
    }
  }, [activeProject, fetchPoints])

  const applyFilter = () => {
    setAppliedFilter(filter.trim())
    setOffset(0)
  }
  const clearFilter = () => {
    setFilter('')
    setAppliedFilter('')
    setOffset(0)
  }

  if (projects !== null && projects.length === 0) {
    return (
      <>
        <div className="page-head"><h1>Chunks</h1></div>
        <section className="panel">
          <div className="empty-state">
            <Inbox size={28} />
            <div><b>No projects yet.</b> Connect a machine and let its agent search a project once — chunks show up here.</div>
            <div className="empty-actions">
              <button className="btn primary" onClick={() => onNavigate('connect')}><Plug size={14} /> Connect a machine</button>
            </div>
          </div>
        </section>
      </>
    )
  }

  const st = activeProject ? reindex.state[activeProject] : undefined

  return (
    <>
      <div className="page-head wrap">
        <h1>Chunks</h1>
        {projects && projects.length > 0 && (
          <select
            className="select-box mono inline-select"
            value={activeProject}
            onChange={(e) => onSelectProject(e.target.value)}
            aria-label="Project"
          >
            {projects.map((p) => (
              <option key={p.project_id} value={p.project_id}>{p.project_id}</option>
            ))}
          </select>
        )}
        <div className="head-actions">
          <button
            className="btn"
            onClick={() => activeProject && reindex.start(activeProject)}
            disabled={!activeProject || st?.busy}
            title="Sync the project's folders if they exist on this server, otherwise re-embed its stored chunks"
          >
            {st?.busy ? <Loader2 size={14} className="spin" /> : <RefreshCw size={14} strokeWidth={1.7} />}
            Re-index
          </button>
        </div>
      </div>

      {project && (
        <section className="panel">
          <div className="panel-head">
            <h2>Sources</h2>
            <span className="hint">
              {formatNumber(project.chunk_count)} chunks · {formatNumber(project.file_count)} files · {formatNumber(project.query_count)} queries
            </span>
          </div>
          <div className="panel-body">
            <SourceList sources={project.sources || []} now={now} detailed />
            <div className="field-hint">
              Folders on client machines sync automatically before each search. Re-index scans folders that exist on this server; otherwise it re-embeds the stored chunks with the current model.
            </div>
            {st && !st.busy && <ReindexStatus st={st} onDismiss={() => reindex.dismiss(activeProject)} />}
          </div>
        </section>
      )}

      <section className="panel">
        <div className="panel-head">
          <h2>All chunks</h2>
          <span className="hint">{appliedFilter ? `filtered: ${appliedFilter}` : `${points.length} shown`}</span>
        </div>
        <div className="panel-body">
          <div className="filter-row">
            <Filter size={14} className="faint" style={{ flex: 'none' }} />
            <input
              className="query-input"
              type="text"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && applyFilter()}
              placeholder="Filter by source file…"
            />
            {filter !== appliedFilter && <button className="btn" onClick={applyFilter}>Apply</button>}
            {appliedFilter && <button className="btn" onClick={clearFilter}>Clear</button>}
          </div>

          {error && <ErrorBox>{error}</ErrorBox>}
          {loading && <Spinner />}

          {!loading && !error && points.length === 0 && (
            <div className="empty-state">
              <Inbox size={28} />
              {appliedFilter ? 'No chunks match this file filter.' : activeProject ? 'This project has no chunks yet.' : 'Select a project.'}
            </div>
          )}

          {points.length > 0 && (
            <div className="chunk-list">
              {points.map((p) => (
                <div className="chunk-row" key={p.id}>
                  <div className="chunk-info">
                    <div className="chunk-header">
                      <span className="fname mono">{p.source_file || p.id}</span>
                      {p.chunk_index && <span className="chunk-idx mono">chunk {p.chunk_index}</span>}
                    </div>
                    {p.content && <div className="chunk-preview mono">{p.content}</div>}
                    <div className="chunk-meta">
                      {p.content_hash && <span className="tag mono">hash: {p.content_hash}</span>}
                      {p.chunk_version && <span className="tag mono">{p.chunk_version}</span>}
                      <span className="tag mono">{p.id}</span>
                    </div>
                  </div>
                  <button
                    className="btn chunk-delete"
                    onClick={() => handleDelete(p.id)}
                    disabled={deletingId === p.id}
                    title="Delete chunk"
                  >
                    <Trash2 size={13} strokeWidth={1.7} />
                  </button>
                </div>
              ))}
            </div>
          )}

          {(points.length > 0 || offset > 0) && (
            <div className="pagination">
              <button className="btn" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - LIMIT))}>
                <ChevronLeft size={14} strokeWidth={1.7} /> Previous
              </button>
              <span className="mono page-info">{points.length ? `${offset + 1}–${offset + points.length}` : `${offset}+`}</span>
              <button className="btn" disabled={points.length < LIMIT} onClick={() => setOffset(offset + LIMIT)}>
                Next <ChevronRight size={14} strokeWidth={1.7} />
              </button>
            </div>
          )}
        </div>
      </section>
    </>
  )
}
