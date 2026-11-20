import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { UploadCloud, FileArchive, X, Play, Loader2, CheckCircle2, ArrowRight, RotateCcw } from 'lucide-react'
import type { Page } from '../App'
import { api, errorText, getToken, type ImportUploadResponse, type Job, type ModelsResponse, type ServerInfo } from '../lib/api'
import { useJob, useJobs } from '../lib/sse'
import { estimateCost, estimateTokens, formatBytes, formatNumber, formatTokens, formatUSD, formatDuration } from '../lib/format'
import { CopyBlock } from '../components/CopyBlock'
import { pushCmd } from '../components/ConnectCommands'
import { JobProgress } from '../components/JobProgress'
import { Badge, ErrorBox, WarnBox } from '../components/ui'

type Stage =
  | { kind: 'idle' }
  | { kind: 'uploading'; file: File; loaded: number; total: number }
  | { kind: 'analyzed'; file: File; upload: ImportUploadResponse; receivedAt: number }
  | { kind: 'importing'; job: Job }

export function Import({ onNavigate }: { onNavigate: (p: Page) => void }) {
  const [stage, setStage] = useState<Stage>({ kind: 'idle' })
  const [error, setError] = useState('')
  const [server, setServer] = useState<ServerInfo | null>(null)
  const [models, setModels] = useState<ModelsResponse | null>(null)
  const abortRef = useRef<AbortController | null>(null)

  useEffect(() => {
    api.server().then(setServer).catch(() => {})
    api.models().then(setModels).catch(() => {})
    return () => abortRef.current?.abort()
  }, [])

  const upload = useCallback(async (file: File) => {
    setError('')
    const ctrl = new AbortController()
    abortRef.current = ctrl
    setStage({ kind: 'uploading', file, loaded: 0, total: file.size })
    try {
      const res = await api.importUpload(
        file,
        (loaded, total) => setStage({ kind: 'uploading', file, loaded, total }),
        { signal: ctrl.signal, gzip: /\.gz$/i.test(file.name) },
      )
      setStage({ kind: 'analyzed', file, upload: res, receivedAt: Date.now() })
    } catch (e) {
      setStage({ kind: 'idle' })
      if (!ctrl.signal.aborted) setError(errorText(e, 'Upload failed'))
    }
  }, [])

  const reset = useCallback(() => {
    if (stage.kind === 'analyzed') api.importDiscard(stage.upload.upload_id).catch(() => {})
    setStage({ kind: 'idle' })
    setError('')
  }, [stage])

  return (
    <>
      <div className="page-head">
        <h1>Import</h1>
        <span className="id mono">local SQLite → {server ? server.store.type : 'server store'}</span>
      </div>

      <div className="grid-2-1 align-start">
        <section className="panel">
          <div className="panel-head">
            <h2>Upload a rag.db</h2>
            {stage.kind !== 'idle' && stage.kind !== 'uploading' && (
              <button className="btn small head-btn" onClick={reset}><RotateCcw size={13} /> Start over</button>
            )}
          </div>
          <div className="panel-body">
            <p className="lead">
              Bring memory from a machine that used the built-in SQLite store. The data is converted automatically into this
              server's store{server?.store.type === 'pgvector' ? ' (PostgreSQL + pgvector)' : ''}: vectors are reused when they were made with
              the same model and dimension as the server ({server ? <span className="mono">{server.embedder.model} · {server.embedder.dim}</span> : '…'}), otherwise
              those chunks are re-embedded.
            </p>
            {error && <ErrorBox>{error}</ErrorBox>}
            {stage.kind === 'idle' && <DropZone onFile={upload} />}
            {stage.kind === 'uploading' && (
              <UploadProgress stage={stage} onCancel={() => abortRef.current?.abort()} />
            )}
            {stage.kind === 'analyzed' && (
              <ImportPlan
                stage={stage}
                server={server}
                models={models}
                onStarted={(job) => setStage({ kind: 'importing', job })}
              />
            )}
            {stage.kind === 'importing' && (
              <ImportRunning job={stage.job} onAnother={() => setStage({ kind: 'idle' })} onOverview={() => onNavigate('overview')} />
            )}
          </div>
        </section>

        <PushCard />
      </div>
    </>
  )
}

// ---------------------------------------------------------------------------

function DropZone({ onFile }: { onFile: (f: File) => void }) {
  const [over, setOver] = useState(false)
  const input = useRef<HTMLInputElement>(null)
  return (
    <div
      className={`dropzone ${over ? 'over' : ''}`}
      onDragOver={(e) => {
        e.preventDefault()
        setOver(true)
      }}
      onDragLeave={() => setOver(false)}
      onDrop={(e) => {
        e.preventDefault()
        setOver(false)
        const f = e.dataTransfer.files?.[0]
        if (f) onFile(f)
      }}
      onClick={() => input.current?.click()}
      role="button"
      tabIndex={0}
      onKeyDown={(e) => {
        if (e.key === 'Enter' || e.key === ' ') input.current?.click()
      }}
    >
      <UploadCloud size={30} strokeWidth={1.5} />
      <div className="dz-title">Drop <span className="mono">rag.db</span> here or click to choose</div>
      <div className="dz-sub">
        Usually at <span className="mono">~/.enowx-rag/rag.db</span> on the old machine · up to ~2 GB · <span className="mono">.db.gz</span> works too
      </div>
      <input
        ref={input}
        type="file"
        accept=".db,.sqlite,.sqlite3,.gz,application/octet-stream,application/gzip"
        hidden
        onChange={(e) => {
          const f = e.target.files?.[0]
          if (f) onFile(f)
          e.target.value = ''
        }}
      />
    </div>
  )
}

function UploadProgress({ stage, onCancel }: { stage: Extract<Stage, { kind: 'uploading' }>; onCancel: () => void }) {
  const pct = stage.total ? Math.round((stage.loaded / stage.total) * 100) : 0
  const analyzing = stage.total > 0 && stage.loaded >= stage.total
  return (
    <div className="upload-box">
      <div className="upload-file">
        <FileArchive size={18} />
        <span className="mono">{stage.file.name}</span>
        <span className="faint mono">{formatBytes(stage.file.size)}</span>
        {!analyzing && (
          <button className="icon-btn tiny" onClick={onCancel} title="Cancel upload"><X size={12} /></button>
        )}
      </div>
      <div className="job-bar-row">
        <div className={`job-bar ${analyzing ? 'indeterminate' : ''}`}><i style={{ width: analyzing ? undefined : `${pct}%` }} /></div>
        <span className="job-count mono">{analyzing ? 'reading…' : `${pct}%`}</span>
      </div>
      <div className="field-hint">
        {analyzing
          ? 'Upload complete — the server is opening the database and listing its projects…'
          : `${formatBytes(stage.loaded)} of ${formatBytes(stage.total)} uploaded`}
      </div>
    </div>
  )
}

function ImportPlan({ stage, server, models, onStarted }: {
  stage: Extract<Stage, { kind: 'analyzed' }>
  server: ServerInfo | null
  models: ModelsResponse | null
  onStarted: (job: Job) => void
}) {
  const up = stage.upload
  const { trackJob } = useJobs()
  const [selected, setSelected] = useState<Set<string>>(() => new Set(up.projects.map((p) => p.project_id)))
  const [rename, setRename] = useState<Record<string, string>>({})
  const [mode, setMode] = useState<'merge' | 'replace'>('merge')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const toggle = (id: string) =>
    setSelected((s) => {
      const n = new Set(s)
      if (n.has(id)) n.delete(id)
      else n.add(id)
      return n
    })

  const chosen = up.projects.filter((p) => selected.has(p.project_id))
  const allChecked = chosen.length === up.projects.length
  const reembedPoints = chosen.filter((p) => !p.reuse_vectors).reduce((s, p) => s + p.points, 0)
  const reusePoints = chosen.filter((p) => p.reuse_vectors).reduce((s, p) => s + p.points, 0)
  // Characters are only known for the whole upload; scale by the re-embed share.
  const chars = up.estimate.reembed_points > 0 ? (up.estimate.reembed_chars * reembedPoints) / up.estimate.reembed_points : 0
  const tokens = estimateTokens(chars)
  const embedInfo = useMemo(
    () => models?.embedding.find((m) => m.id === server?.embedder.model) || server?.embedder.info,
    [models, server],
  )
  const cost = estimateCost(tokens, embedInfo?.price_per_mtok || 0)
  const existing = chosen.filter((p) => p.exists && !(rename[p.project_id] || '').trim())
  const remaining = Math.max(0, up.expires_in_s - Math.floor((Date.now() - stage.receivedAt) / 1000))

  const start = async () => {
    setBusy(true)
    setError('')
    const renames: Record<string, string> = {}
    for (const p of chosen) {
      const r = (rename[p.project_id] || '').trim()
      if (r && r !== p.project_id) renames[p.project_id] = r
    }
    try {
      const res = await api.importStart({
        upload_id: up.upload_id,
        projects: allChecked ? undefined : chosen.map((p) => p.project_id),
        mode,
        rename: Object.keys(renames).length ? renames : undefined,
      })
      trackJob(res.job)
      onStarted(res.job)
    } catch (e) {
      setError(errorText(e, 'Could not start the import'))
    } finally {
      setBusy(false)
    }
  }

  if (up.projects.length === 0) {
    return (
      <WarnBox title="No projects found in this file.">
        Make sure it is an enowx-rag SQLite store (<span className="mono">rag.db</span>), not another database.
      </WarnBox>
    )
  }

  return (
    <div className="import-plan">
      <div className="upload-file">
        <FileArchive size={18} />
        <span className="mono">{stage.file.name}</span>
        <span className="faint mono">{formatBytes(up.size_bytes)}</span>
        <span className="faint">· {up.projects.length} project{up.projects.length === 1 ? '' : 's'} · upload kept for {formatDuration(remaining)}</span>
      </div>

      <div className="table-scroll">
        <table className="dtable compact">
          <thead>
            <tr>
              <th className="check-col">
                <input
                  type="checkbox"
                  checked={allChecked}
                  onChange={() => setSelected(allChecked ? new Set() : new Set(up.projects.map((p) => p.project_id)))}
                  aria-label="Select all"
                />
              </th>
              <th>Project</th>
              <th className="num">Chunks</th>
              <th>Embedded with</th>
              <th>Vectors</th>
              <th>Import as</th>
            </tr>
          </thead>
          <tbody>
            {up.projects.map((p) => {
              const on = selected.has(p.project_id)
              const target = (rename[p.project_id] || '').trim() || p.project_id
              return (
                <tr key={p.project_id} className={on ? '' : 'muted-row'}>
                  <td className="check-col">
                    <input type="checkbox" checked={on} onChange={() => toggle(p.project_id)} aria-label={`Import ${p.project_id}`} />
                  </td>
                  <td data-label="Project" className="mono strong">
                    {p.project_id}
                    {p.exists && !(rename[p.project_id] || '').trim() && (
                      <div><Badge tone="warn">already exists</Badge></div>
                    )}
                  </td>
                  <td data-label="Chunks" className="num mono">{formatNumber(p.points)}</td>
                  <td data-label="Embedded with" className="mono faint">{p.embed_model || '?'} · {p.embed_dim || '?'}</td>
                  <td data-label="Vectors">
                    {p.reuse_vectors ? (
                      <Badge tone="good" title="Same model and dimension as the server">vectors reused — no re-embed cost</Badge>
                    ) : (
                      <Badge tone="accent" title={`Will be re-embedded with ${server?.embedder.model || 'the server model'}`}>will be re-embedded</Badge>
                    )}
                  </td>
                  <td data-label="Import as">
                    <input
                      className="input mono small-input"
                      value={rename[p.project_id] || ''}
                      placeholder={p.project_id}
                      onChange={(e) => setRename((r) => ({ ...r, [p.project_id]: e.target.value }))}
                      disabled={!on}
                      aria-label={`New name for ${p.project_id}`}
                      title={target !== p.project_id ? `Imported as ${target}` : 'Keep the same name'}
                    />
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>

      <div className="field">
        <label>When a project already exists on the server</label>
        <div className="pill-group">
          <button type="button" className={`pill ${mode === 'merge' ? 'active' : ''}`} onClick={() => setMode('merge')}>Merge</button>
          <button type="button" className={`pill ${mode === 'replace' ? 'active' : ''}`} onClick={() => setMode('replace')}>Replace</button>
        </div>
        <div className="field-hint">
          {mode === 'merge'
            ? 'Merge adds the imported chunks and updates chunks that already exist; nothing on the server is removed.'
            : 'Replace deletes the existing project on the server first, then imports — use it to overwrite stale data.'}
        </div>
      </div>

      {existing.length > 0 && (
        <WarnBox title={`${existing.length} project${existing.length === 1 ? '' : 's'} already exist${existing.length === 1 ? 's' : ''} on the server:`}>
          <span className="mono">{existing.map((p) => p.project_id).join(', ')}</span>.{' '}
          {mode === 'replace' ? (existing.length === 1 ? 'It will be wiped and replaced.' : 'They will be wiped and replaced.') : (existing.length === 1 ? 'Its chunks will be merged.' : 'Their chunks will be merged.')} Rename to import side by side.
        </WarnBox>
      )}

      <div className="impact-grid">
        <div><span className="ik">Chunks to import</span><span className="iv mono">{formatNumber(reusePoints + reembedPoints)}</span></div>
        <div><span className="ik">Vectors reused</span><span className="iv mono">{formatNumber(reusePoints)}</span></div>
        <div><span className="ik">To re-embed</span><span className="iv mono">{formatNumber(reembedPoints)}</span></div>
        <div>
          <span className="ik">Re-embed estimate</span>
          <span className="iv mono">{reembedPoints ? `≈ ${formatTokens(tokens)} tok · ${embedInfo ? formatUSD(cost) : '?'}` : '$0'}</span>
        </div>
      </div>
      {reembedPoints > 0 && (
        <div className="field-hint">
          Re-embedding uses {server?.embedder.model || 'the server model'}; estimate ≈ characters ÷ 4 tokens.
          {embedInfo?.free_mtok ? ' Voyage free-tier tokens, if unused, cover this first.' : ''}
        </div>
      )}

      {error && <ErrorBox>{error}</ErrorBox>}
      <div className="form-actions">
        <button className="btn primary" onClick={start} disabled={busy || chosen.length === 0}>
          {busy ? <Loader2 size={14} className="spin" /> : <Play size={14} />}
          Import {chosen.length} project{chosen.length === 1 ? '' : 's'}
        </button>
      </div>
    </div>
  )
}

function ImportRunning({ job, onAnother, onOverview }: { job: Job; onAnother: () => void; onOverview: () => void }) {
  const live = useJob(job.id) || job
  return (
    <div className="import-running">
      <JobProgress jobId={job.id} initial={job} />
      {live.state === 'done' && (
        <div className="success-box">
          <CheckCircle2 size={16} />
          <span>Import finished — the projects are on the server. Chunks that need re-embedding continue in the background.</span>
        </div>
      )}
      {live.state !== 'running' && (
        <div className="form-actions">
          <button className="btn primary" onClick={onOverview}>Go to Overview <ArrowRight size={14} /></button>
          <button className="btn" onClick={onAnother}>Import another file</button>
        </div>
      )}
      {live.state === 'running' && (
        <div className="field-hint">You can leave this page — the import keeps running on the server and shows up under Jobs on the Overview.</div>
      )}
    </div>
  )
}

function PushCard() {
  const url = window.location.origin
  const token = getToken()
  return (
    <section className="panel">
      <div className="panel-head">
        <h2>Push from your machine</h2>
      </div>
      <div className="panel-body">
        <p className="lead">
          Easier for big stores: run this on the machine that has the data. It uploads <span className="mono">~/.enowx-rag/rag.db</span>,
          starts the import and also sends which folder each project lives in, so Sources show up right away.
        </p>
        <CopyBlock title="terminal" code={pushCmd(url, token)} secret={token || undefined} />
        <div className="field-hint">
          Needs <span className="mono">enowx-rag</span> installed on that machine — see Connect for the install command.
        </div>
      </div>
    </section>
  )
}
