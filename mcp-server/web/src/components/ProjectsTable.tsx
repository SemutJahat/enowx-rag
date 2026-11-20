import { Fragment, useState } from 'react'
import { Loader2, RefreshCw, Trash2 } from 'lucide-react'
import { api, errorText, type ProjectSummary } from '../lib/api'
import { formatNumber, fullTime, plural, relativeTime } from '../lib/format'
import { ReindexStatus, SourceList, useReindex } from './Reindex'
import { ConfirmDialog } from './ui'

interface ProjectsTableProps {
  projects: ProjectSummary[]
  now: number
  onOpenProject: (id: string) => void
  onChanged: () => void
}

export function ProjectsTable({ projects, now, onOpenProject, onChanged }: ProjectsTableProps) {
  const reindex = useReindex()
  const [confirm, setConfirm] = useState<ProjectSummary | null>(null)
  const [deleting, setDeleting] = useState(false)
  const [deleteError, setDeleteError] = useState('')

  const doDelete = async () => {
    if (!confirm) return
    setDeleting(true)
    setDeleteError('')
    try {
      await api.deleteProject(confirm.project_id)
      setConfirm(null)
      onChanged()
    } catch (e) {
      setDeleteError(errorText(e, 'Delete failed'))
    } finally {
      setDeleting(false)
    }
  }

  return (
    <>
      <div className="table-wrap">
      <table className="dtable">
        <thead>
          <tr>
            <th>Project</th>
            <th className="num">Chunks</th>
            <th className="num">Files</th>
            <th>Sources</th>
            <th className="num">Queries</th>
            <th>Last query</th>
            <th className="actions-col"><span className="sr-only">Actions</span></th>
          </tr>
        </thead>
        <tbody>
          {projects.map((p) => {
            const st = reindex.state[p.project_id]
            return (
              <Fragment key={p.project_id}>
                <tr>
                  <td data-label="Project">
                    <button type="button" className="link-btn mono strong" onClick={() => onOpenProject(p.project_id)} title="Browse chunks">
                      {p.project_id}
                    </button>
                  </td>
                  <td data-label="Chunks" className="num mono">{formatNumber(p.chunk_count)}</td>
                  <td data-label="Files" className="num mono">{formatNumber(p.file_count)}</td>
                  <td data-label="Sources"><SourceList sources={p.sources || []} now={now} /></td>
                  <td data-label="Queries" className="num mono">{formatNumber(p.query_count)}</td>
                  <td data-label="Last query" className="faint" title={fullTime(p.last_query_at)}>
                    {p.last_query_at ? relativeTime(p.last_query_at, now) : 'never'}
                  </td>
                  <td className="actions-col">
                    <div className="row-actions">
                      <button
                        className="btn small"
                        onClick={() => reindex.start(p.project_id)}
                        disabled={st?.busy}
                        title="Sync the project's folders if they exist on this server, otherwise re-embed its stored chunks"
                      >
                        {st?.busy ? <Loader2 size={13} className="spin" /> : <RefreshCw size={13} />}
                        Re-index
                      </button>
                      <button className="btn small danger-ghost" onClick={() => { setDeleteError(''); setConfirm(p) }} title="Delete project">
                        <Trash2 size={13} />
                      </button>
                    </div>
                  </td>
                </tr>
                {st && !st.busy && (
                  <tr className="sub-row">
                    <td colSpan={7}>
                      <ReindexStatus st={st} onDismiss={() => reindex.dismiss(p.project_id)} />
                    </td>
                  </tr>
                )}
              </Fragment>
            )
          })}
        </tbody>
      </table>
      </div>

      {confirm && (
        <ConfirmDialog
          title={`Delete ${confirm.project_id}?`}
          confirmLabel="Delete project"
          danger
          busy={deleting}
          error={deleteError}
          onCancel={() => setConfirm(null)}
          onConfirm={doDelete}
        >
          <p>
            This permanently removes <b>{plural(confirm.chunk_count, 'chunk')}</b> and the project's saved notes from the
            server's store. It cannot be undone.
          </p>
          <p className="faint">
            Files on your machines are not touched. A connected machine that still uses this project may index it again on its next sync.
          </p>
        </ConfirmDialog>
      )}
    </>
  )
}
