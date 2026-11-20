// API client: typed wrappers for every REST endpoint of the enowx-rag v2
// "host mode" API. Every request carries the admin token (when one is stored)
// as `Authorization: Bearer`; a 401 dispatches UNAUTHORIZED_EVENT so the app
// can show the login screen.

// ---------------------------------------------------------------------------
// Auth token
// ---------------------------------------------------------------------------

export const TOKEN_KEY = 'enowx-admin-token'
/** A freshly generated token that may only become active after a restart. */
const PENDING_TOKEN_KEY = 'enowx-admin-token-pending'
export const UNAUTHORIZED_EVENT = 'enowx:unauthorized'

export function getToken(): string {
  try {
    return localStorage.getItem(TOKEN_KEY) || ''
  } catch {
    return ''
  }
}

export function setToken(token: string): void {
  try {
    if (token) localStorage.setItem(TOKEN_KEY, token)
    else localStorage.removeItem(TOKEN_KEY)
  } catch {
    // storage unavailable: the token lives only for this request
  }
}

export function clearToken(): void {
  setToken('')
  try {
    sessionStorage.removeItem(PENDING_TOKEN_KEY)
  } catch {
    // ignore
  }
}

/**
 * Remember a newly generated admin token. It is swapped in automatically the
 * first time the current token is rejected (e.g. after the server restarts
 * with the new token), so the user is not bounced to the login screen.
 */
export function setPendingToken(token: string): void {
  try {
    sessionStorage.setItem(PENDING_TOKEN_KEY, token)
  } catch {
    // ignore
  }
}

function takePendingToken(): string {
  try {
    const t = sessionStorage.getItem(PENDING_TOKEN_KEY) || ''
    sessionStorage.removeItem(PENDING_TOKEN_KEY)
    return t
  } catch {
    return ''
  }
}

export function authHeaders(token: string = getToken()): Record<string, string> {
  return token ? { Authorization: `Bearer ${token}` } : {}
}

// ---------------------------------------------------------------------------
// Errors
// ---------------------------------------------------------------------------

export class ApiError extends Error {
  status: number
  hint?: string
  /** The answer came from a proxy/CDN in front of enowx-rag, not from it. */
  proxy: boolean
  /** The parsed JSON body of the error, when there is one. */
  data?: Record<string, unknown>
  constructor(message: string, status: number, hint?: string, opts: { proxy?: boolean; data?: Record<string, unknown> } = {}) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.hint = hint
    this.proxy = !!opts.proxy
    this.data = opts.data
  }
}

/** Human-readable error text including the server's hint, if any. */
export function errorText(e: unknown, fallback = 'Request failed'): string {
  if (e instanceof ApiError) return e.hint ? `${e.message} — ${e.hint}` : e.message
  if (e instanceof Error) return e.message
  return fallback
}

export function isAuthError(e: unknown): boolean {
  // A proxy's 403 (Cloudflare WAF, IP allowlist) is not about the token.
  return e instanceof ApiError && !e.proxy && (e.status === 401 || e.status === 403)
}

function notifyUnauthorized() {
  window.dispatchEvent(new CustomEvent(UNAUTHORIZED_EVENT))
}

async function parseError(resp: Response): Promise<ApiError> {
  const text = await resp.text().catch(() => '')
  return errorFrom(resp.status, text, (h) => resp.headers.get(h))
}

/**
 * Turn an error answer into an ApiError. enowx-rag's own errors are JSON
 * ({error, hint}); anything else came from a proxy or CDN in front of it
 * (Cloudflare, Traefik, nginx) and is explained as such.
 */
function errorFrom(status: number, text: string, header: (name: string) => string | null): ApiError {
  let body: unknown
  try {
    body = text ? JSON.parse(text) : undefined
  } catch {
    body = undefined
  }
  if (body && typeof body === 'object') {
    const b = body as Record<string, unknown>
    if (typeof b.error === 'string') {
      return new ApiError(b.error, status, typeof b.hint === 'string' ? b.hint : undefined, { data: b })
    }
  }
  const p = proxyProblem(status, header)
  return new ApiError(p.message, status, p.hint, { proxy: true })
}

function proxyProblem(status: number, header: (name: string) => string | null): { message: string; hint?: string } {
  const rayHeader = header('cf-ray')
  const cf = !!rayHeader || (header('server') || '').toLowerCase() === 'cloudflare'
  const who = cf ? 'Cloudflare' : 'A proxy in front of enowx-rag'
  const ray = rayHeader ? ` Ray ID ${rayHeader.split('-')[0]}.` : ''
  const origin: Record<number, string> = {
    520: 'the server sent an empty or invalid answer',
    521: 'the server refused the connection (is its reverse proxy running?)',
    522: 'the connection to the server timed out (a firewall may drop Cloudflare’s IPs)',
    523: 'the server is unreachable (check the DNS record’s IP)',
    524: 'the server took longer than 100 seconds to answer',
    525: 'the TLS handshake with the server failed',
    526: 'the server’s certificate is not valid for SSL mode Full (strict)',
    530: 'Cloudflare cannot reach the server (DNS record missing?)',
  }
  if (cf && origin[status]) {
    return { message: `Cloudflare could not reach enowx-rag (HTTP ${status})`, hint: `${origin[status]}.${ray}` }
  }
  switch (status) {
    case 413:
      return {
        message: `${who} refused the request as too large (HTTP 413)`,
        hint: `${cf ? 'Cloudflare Free and Pro accept 100 MB per request.' : 'The proxy caps the request size.'}${ray}`,
      }
    case 403:
      return {
        message: `${who} blocked the request before it reached enowx-rag (HTTP 403)`,
        hint: `Not a token problem: check the WAF/firewall rules, IP allowlists and bot protection for this hostname.${ray}`,
      }
    case 429:
      return { message: `${who} rate-limited the request (HTTP 429)`, hint: `Wait a moment and try again.${ray}` }
    case 502:
    case 503:
    case 504:
      return {
        message: `enowx-rag is not answering behind the proxy (HTTP ${status})`,
        hint: `It may be restarting or down — check the container logs.${ray}`,
      }
  }
  return { message: `HTTP ${status}`, hint: `${who} answered with a page instead of enowx-rag’s JSON.${ray}` }
}

// ---------------------------------------------------------------------------
// Shared types (see the API contract)
// ---------------------------------------------------------------------------

export interface Event {
  type: string
  timestamp: string
  data?: Record<string, unknown>
}

export type JobKind = 'reindex' | 'reembed' | 'import' | 'refresh'
export type JobState = 'running' | 'done' | 'failed'

export interface Job {
  id: string
  kind: JobKind
  project?: string
  state: JobState
  message: string
  done: number
  total: number
  started_at: string
  finished_at?: string
  result?: Record<string, unknown>
  error?: string
}

export interface ModelInfo {
  id: string
  family: string
  status: 'latest' | 'older' | 'deprecated'
  recommended: boolean
  context_tokens: number
  default_dim: number
  dims: number[]
  price_per_mtok: number
  free_mtok: number
  best_for: string
  notes?: string
}

export interface RerankInfo {
  id: string
  status: 'latest' | 'older'
  recommended: boolean
  context_tokens: number
  query_tokens: number
  price_per_mtok: number
  free_mtok: number
  best_for: string
}

export interface Health {
  ok: boolean
  version: string
  boot_id: string
  uptime_s: number
  auth_required: boolean
}

export interface SetupStatus {
  configured: boolean
  config_path: string
  config_file_exists: boolean
  missing: string[]
  env_locked: string[]
  auth_required: boolean
  restart_supported: boolean
}

export type StoreType = 'pgvector' | 'sqlite' | 'qdrant' | 'chroma' | 'remote'

export interface StoreStatus {
  type: StoreType
  ok: boolean
  error?: string
  target: string
  server_version?: string
  extension_version?: string
  table?: string
  dim?: number
  size_bytes?: number
  rows?: number
  latency_ms?: number
}

export interface ServerInfo {
  version: string
  boot_id: string
  started_at: string
  uptime_s: number
  hostname: string
  os: string
  go_version: string
  auth_required: boolean
  config_path: string
  env_locked: string[]
  restart_supported: boolean
  store: StoreStatus
  embedder: { type: 'voyage' | 'openai' | 'tei'; model: string; dim: number; key_set: boolean; info?: ModelInfo }
  reranker: { enabled: boolean; model: string; info?: RerankInfo; reason?: string }
  auto_sync: { enabled: boolean; interval: string }
  content_chars: number
  stale_points: number
  jobs_running: number
}

export interface ModelsResponse {
  embedding: ModelInfo[]
  rerank: RerankInfo[]
  compat_note: string
  source: string
}

export interface ModelTestRequest {
  voyage_api_key?: string
  embed_model: string
  dim?: number
  rerank_model?: string
}

export interface ModelTestResponse {
  embed: { ok: boolean; message: string; latency_ms: number; dim?: number; tokens?: number }
  rerank?: { ok: boolean; message: string; latency_ms: number } | null
}

export interface SyncResult {
  indexed: number
  updated: number
  deleted: number
  files_scanned: number
  skipped: number
}

export interface ProjectSource {
  host: string
  dir: string
  on_server: boolean
  last_sync?: string
  last_result?: SyncResult
}

export interface ProjectSummary {
  project_id: string
  chunk_count: number
  file_count: number
  sources: ProjectSource[]
  query_count: number
  last_query_at?: string
}

export interface PointInfo {
  id: string
  source_file?: string
  content_hash?: string
  chunk_version?: string
  doc_id?: string
  content?: string
  chunk_index?: string
}

export interface ReindexRequest {
  mode?: 'auto' | 'files' | 'reembed'
  directory?: string
}

export interface ReindexResponse {
  job: Job
  mode: 'files' | 'reembed'
  note: string
}

export interface QueryComposition {
  hybrid: boolean
  reranked: boolean
  candidates: number
  results: number
  dense_count: number
  lexical_count: number
  rerank_moved: number
}

export interface TimelineBucket {
  t: string
  count: number
  avg_latency_ms: number
}

export interface RecentQuery {
  ts: string
  project: string
  query: string
  latency_ms: number
  results: number
  reranked: boolean
  hybrid: boolean
  client: string
}

export interface MetricsResponse {
  query_count: number
  avg_latency_ms: number
  p50_latency_ms: number
  p95_latency_ms: number
  tokens_total: number
  tokens_embed: number
  tokens_rerank: number
  persistent: boolean
  backend: string
  last_query?: QueryComposition
  queries_24h: number
  timeline_24h: TimelineBucket[]
  per_project: Array<{ project_id: string; count: number; last_at?: string }>
  recent_queries: RecentQuery[]
}

export interface StatsResponse {
  total_projects: number
  total_chunks: number
  embed_model: string
  projects: Array<{ project_id: string; chunk_count: number }>
}

export interface ImportProject {
  project_id: string
  points: number
  embed_model: string
  embed_dim: number
  reuse_vectors: boolean
  exists: boolean
}

export interface ImportUploadResponse {
  upload_id: string
  size_bytes: number
  expires_in_s: number
  projects: ImportProject[]
  estimate: { reembed_points: number; reembed_chars: number }
}

export interface ImportStartRequest {
  upload_id: string
  projects?: string[]
  mode: 'merge' | 'replace'
  rename?: Record<string, string>
}

export interface ConfigMasked {
  vector_store: string
  embedder: string
  pgvector_dsn: string
  sqlite_path: string
  qdrant_url: string
  chroma_url: string
  tei_url: string
  voyage_model: string
  voyage_dim: number
  voyage_api_key: string
  openai_base_url: string
  openai_model: string
  openai_api_key: string
  reranker_model: string
  auto_sync_interval: string
  admin_token_set: boolean
  admin_token: string
  env_locked: string[]
}

export interface ConfigUpdate {
  voyage_api_key?: string
  voyage_model?: string
  voyage_dim?: number
  reranker_model?: string
  auto_sync_interval?: string
  openai_api_key?: string
  openai_model?: string
  openai_base_url?: string
  qdrant_api_key?: string
  pgvector_dsn?: string
  admin_token?: string
  restart?: boolean
}

export interface ConfigUpdateResponse {
  status: string
  restart_required: boolean
  restarting: boolean
  warnings: string[]
}

export interface ComponentResult {
  ok: boolean
  message: string
  latency_ms: number
}

export interface SetupTestResponse {
  vector_store: ComponentResult
  embedder: ComponentResult
  reranker?: ComponentResult | null
}

export interface SetupApplyRequest {
  vector_store: string
  embedder: string
  voyage_api_key?: string
  voyage_model?: string
  voyage_dim?: number
  reranker_model?: string
  openai_api_key?: string
  openai_model?: string
  openai_base_url?: string
  openai_dim?: number
  pgvector_dsn?: string
  sqlite_path?: string
  qdrant_url?: string
  qdrant_api_key?: string
  chroma_url?: string
  tei_url?: string
  restart?: boolean
}

export interface SetupApplyResponse {
  status: string
  path: string
  restarting: boolean
}

export interface DetectResponse {
  store: {
    type: string
    from_env: boolean
    dsn_masked?: string
    ok: boolean
    message: string
    server_version?: string
    extension_version?: string
  }
  voyage_key_from_env: boolean
  voyage_key_set: boolean
  sqlite_store?: { path: string; points: number }
  suggested: {
    vector_store: string
    embedder: 'voyage'
    voyage_model: string
    voyage_dim: number
    reranker_model: string
  }
}

export interface GenTokenResponse {
  token: string
  env_override: boolean
  note: string
}

// ---------------------------------------------------------------------------
// Transport
// ---------------------------------------------------------------------------

const API_BASE = '/api'

interface RequestOpts {
  method?: string
  body?: unknown
  signal?: AbortSignal
  /** Do not raise the global "unauthorized" event on 401 (used by login). */
  quiet401?: boolean
  /** Use this token instead of the stored one. */
  token?: string
}

async function request<T>(path: string, opts: RequestOpts = {}, retried = false): Promise<T> {
  const headers: Record<string, string> = { ...authHeaders(opts.token ?? getToken()) }
  let body: BodyInit | undefined
  if (opts.body !== undefined) {
    headers['Content-Type'] = 'application/json'
    body = JSON.stringify(opts.body)
  }
  const resp = await fetch(`${API_BASE}${path}`, {
    method: opts.method || 'GET',
    headers,
    body,
    signal: opts.signal,
  })
  if (resp.status === 401 && !opts.quiet401 && opts.token === undefined) {
    // A token generated earlier may have become active (e.g. after restart).
    const pending = takePendingToken()
    if (!retried && pending && pending !== getToken()) {
      setToken(pending)
      return request<T>(path, opts, true)
    }
    notifyUnauthorized()
  }
  if (!resp.ok) throw await parseError(resp)
  if (resp.status === 204) return undefined as T
  const text = await resp.text()
  return (text ? JSON.parse(text) : undefined) as T
}

const enc = encodeURIComponent

/** Suggested part size; the server's answer wins. Well under Cloudflare's 100 MB. */
const PART_BYTES = 32 * 1024 * 1024
const MIN_PART_BYTES = 1024 * 1024

/**
 * Send one request with upload progress. XMLHttpRequest (not fetch) because
 * only XHR exposes upload progress events.
 */
function xhrSend<T>(
  method: string,
  path: string,
  body: Blob,
  headers: Record<string, string>,
  onProgress: (loaded: number) => void,
  signal?: AbortSignal,
): Promise<T> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open(method, `${API_BASE}${path}`)
    const token = getToken()
    if (token) xhr.setRequestHeader('Authorization', `Bearer ${token}`)
    for (const [k, v] of Object.entries(headers)) xhr.setRequestHeader(k, v)
    xhr.upload.onprogress = (e) => onProgress(e.loaded)
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        try {
          resolve((xhr.responseText ? JSON.parse(xhr.responseText) : undefined) as T)
        } catch {
          reject(new ApiError('The server answered with a page instead of JSON', xhr.status,
            'A login page or another site sits in front of enowx-rag.', { proxy: true }))
        }
        return
      }
      const err = errorFrom(xhr.status, xhr.responseText, (h) => xhr.getResponseHeader(h))
      if (xhr.status === 401 && !err.proxy) notifyUnauthorized()
      reject(err)
    }
    xhr.onerror = () => reject(new ApiError('Network error during upload', 0, 'Check the connection to the server and try again.'))
    xhr.onabort = () => reject(new ApiError('Upload cancelled', 0))
    if (signal) {
      if (signal.aborted) {
        xhr.abort()
        return
      }
      signal.addEventListener('abort', () => xhr.abort(), { once: true })
    }
    xhr.send(body)
  })
}

const sleep = (ms: number, signal?: AbortSignal) =>
  new Promise<void>((resolve, reject) => {
    const t = setTimeout(resolve, ms)
    signal?.addEventListener('abort', () => {
      clearTimeout(t)
      reject(new ApiError('Upload cancelled', 0))
    }, { once: true })
  })

/**
 * Upload a SQLite store (raw or .gz) with progress. It goes in parts so that
 * no single request exceeds a proxy's limit (Cloudflare: 100 MB); a part is
 * retried when the connection drops and split when a proxy refuses its size.
 * Servers without part uploads get the file in one request.
 */
async function uploadImport(
  file: Blob,
  onProgress: (loaded: number, total: number) => void,
  opts: { signal?: AbortSignal; gzip?: boolean } = {},
): Promise<ImportUploadResponse> {
  const { signal, gzip } = opts
  let start: { upload_id: string; part_bytes?: number; max_part_bytes?: number }
  try {
    start = await request('/import/parts', { method: 'POST', signal })
  } catch (e) {
    if (e instanceof ApiError && !e.proxy && (e.status === 404 || e.status === 405)) {
      const headers: Record<string, string> = { 'Content-Type': 'application/octet-stream' }
      if (gzip) headers['Content-Encoding'] = 'gzip'
      return xhrSend('POST', '/import/upload', file, headers, (n) => onProgress(n, file.size), signal)
    }
    throw e
  }
  const id = enc(start.upload_id)
  let part = Math.min(start.part_bytes || PART_BYTES, start.max_part_bytes || Infinity)
  let offset = 0
  let fails = 0
  try {
    while (offset < file.size) {
      const end = Math.min(offset + part, file.size)
      const from = offset
      try {
        const r = await xhrSend<{ received: number }>('PUT', `/import/parts/${id}?offset=${from}`, file.slice(from, end),
          { 'Content-Type': 'application/octet-stream' }, (n) => onProgress(from + n, file.size), signal)
        offset = r.received
        fails = 0
      } catch (e) {
        if (signal?.aborted || !(e instanceof ApiError)) throw e
        if (e.status === 413 && part > MIN_PART_BYTES) {
          part = Math.max(Math.floor(part / 2), MIN_PART_BYTES)
          continue
        }
        if (e.status === 409 && typeof e.data?.received === 'number') {
          offset = e.data.received
          continue
        }
        const transient = [0, 408, 429, 502, 503, 504, 520, 521, 522, 523, 524, 530].includes(e.status)
        if (transient && e.message !== 'Upload cancelled' && fails < 5) {
          fails++
          await sleep(fails * fails * 1000, signal)
          continue
        }
        throw e
      }
    }
    onProgress(file.size, file.size)
    return await request<ImportUploadResponse>(`/import/parts/${id}/finish?size=${file.size}`, { method: 'POST', signal })
  } catch (e) {
    request(`/import/parts/${id}`, { method: 'DELETE' }).catch(() => {})
    throw e
  }
}

// ---------------------------------------------------------------------------
// Endpoints
// ---------------------------------------------------------------------------

export const api = {
  // Open endpoints
  health: (signal?: AbortSignal) => request<Health>('/health', { signal, quiet401: true }),
  setupStatus: () => request<SetupStatus>('/setup/status', { quiet401: true }),

  /** Validate a candidate admin token (GET /api/server with that token). */
  validateToken: (token: string) => request<ServerInfo>('/server', { token }),

  // Monitoring
  server: () => request<ServerInfo>('/server'),
  metrics: () => request<MetricsResponse>('/metrics'),
  stats: () => request<StatsResponse>('/stats'),
  activity: (limit = 50) => request<Event[]>(`/activity?limit=${limit}`),
  jobs: () => request<Job[]>('/jobs'),
  job: (id: string) => request<Job>(`/jobs/${enc(id)}`),

  // Models
  models: () => request<ModelsResponse>('/models'),
  testModels: (body: ModelTestRequest) => request<ModelTestResponse>('/models/test', { method: 'POST', body }),

  // Projects
  listProjects: () => request<ProjectSummary[]>('/projects'),
  createProject: (id: string) =>
    request<{ status: string; project_id: string }>(`/projects/${enc(id)}`, { method: 'POST' }),
  deleteProject: (id: string) =>
    request<{ status: string; project_id: string }>(`/projects/${enc(id)}`, { method: 'DELETE' }),
  reindex: (id: string, body?: ReindexRequest) =>
    request<ReindexResponse>(`/projects/${enc(id)}/reindex`, { method: 'POST', body }),
  listPoints: (id: string, params?: { source_file?: string; offset?: number; limit?: number }) => {
    const qs = new URLSearchParams()
    if (params?.source_file) qs.set('source_file', params.source_file)
    if (params?.offset !== undefined) qs.set('offset', String(params.offset))
    if (params?.limit !== undefined) qs.set('limit', String(params.limit))
    const q = qs.toString()
    return request<PointInfo[]>(`/projects/${enc(id)}/points${q ? '?' + q : ''}`)
  },
  deletePoint: (id: string, pointId: string) =>
    request<{ status: string; project_id: string; point_id: string }>(
      `/projects/${enc(id)}/points/${enc(pointId)}`,
      { method: 'DELETE' },
    ),

  // Import
  importUpload: uploadImport,
  importStart: (body: ImportStartRequest) => request<{ job: Job }>('/import/start', { method: 'POST', body }),
  importDiscard: (uploadId: string) =>
    request<unknown>(`/import/upload/${enc(uploadId)}`, { method: 'DELETE' }),

  // Settings / setup
  getConfig: () => request<ConfigMasked>('/setup/config'),
  updateConfig: (patch: ConfigUpdate) =>
    request<ConfigUpdateResponse>('/setup/config', { method: 'POST', body: patch }),
  setupTest: (cfg: SetupApplyRequest) => request<SetupTestResponse>('/setup/test', { method: 'POST', body: cfg }),
  setupApply: (cfg: SetupApplyRequest) => request<SetupApplyResponse>('/setup/apply', { method: 'POST', body: cfg }),
  detect: () => request<DetectResponse>('/setup/detect'),
  genToken: () => request<GenTokenResponse>('/setup/gen-token', { method: 'POST' }),
  restartServer: () => request<{ restarting: boolean }>('/server/restart', { method: 'POST' }),

  // Docs (served by the backend as markdown)
  docsList: () => request<{ id: string; title: string }[]>('/docs'),
  docsSection: async (id: string): Promise<string> => {
    const r = await fetch(`${API_BASE}/docs/${enc(id)}`, { headers: authHeaders() })
    if (r.status === 401) notifyUnauthorized()
    if (!r.ok) throw await parseError(r)
    return r.text()
  },
}

// ---------------------------------------------------------------------------
// Env-locked settings
// ---------------------------------------------------------------------------

/** Settings key → the environment variable that locks it. */
export const ENV_VAR_FOR: Record<string, string> = {
  vector_store: 'RAG_VECTOR_STORE',
  embedder: 'RAG_EMBEDDER',
  pgvector_dsn: 'RAG_PGVECTOR_DSN',
  voyage_api_key: 'RAG_VOYAGE_API_KEY',
  voyage_model: 'RAG_VOYAGE_MODEL',
  voyage_dim: 'RAG_VECTOR_DIM',
  reranker_model: 'RAG_RERANKER_MODEL',
  auto_sync_interval: 'RAG_AUTO_SYNC_INTERVAL',
  admin_token: 'RAG_ADMIN_TOKEN',
  sqlite_path: 'RAG_SQLITE_PATH',
}

export function envVarFor(key: string): string {
  return ENV_VAR_FOR[key] || `RAG_${key.toUpperCase()}`
}
