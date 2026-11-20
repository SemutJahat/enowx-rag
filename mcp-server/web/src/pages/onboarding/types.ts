// Shared types for the onboarding wizard.

import type { DetectResponse, ModelsResponse, SetupApplyRequest, SetupStatus } from '../../lib/api'

export type EmbedderProvider = 'voyage' | 'tei' | 'openai'

/** Draft configuration collected across the wizard steps. */
export interface DraftConfig {
  /** Set once the draft has been pre-filled from /api/setup/detect. */
  prefilled: boolean
  vectorStore: string // 'pgvector' | 'sqlite' (or another type detected from the environment)
  pgvectorDSN: string
  sqlitePath: string
  embedder: EmbedderProvider
  voyageAPIKey: string
  voyageModel: string
  voyageDim: number
  rerankerModel: string // "" = off
  teiURL: string
  openaiAPIKey: string
  openaiModel: string
  openaiBaseURL: string
  openaiDim: number
}

export const defaultDraft: DraftConfig = {
  prefilled: false,
  vectorStore: 'pgvector',
  pgvectorDSN: '',
  sqlitePath: '',
  embedder: 'voyage',
  voyageAPIKey: '',
  voyageModel: 'voyage-4',
  voyageDim: 1024,
  rerankerModel: 'rerank-3',
  teiURL: 'http://localhost:8081',
  openaiAPIKey: '',
  openaiModel: 'text-embedding-3-small',
  openaiBaseURL: 'https://api.openai.com/v1',
  openaiDim: 0,
}

/** Keys never written to localStorage. */
export const SECRET_FIELDS: (keyof DraftConfig)[] = ['voyageAPIKey', 'openaiAPIKey', 'pgvectorDSN']

export const STEPS = ['welcome', 'database', 'models', 'review', 'connect', 'done'] as const
export type Step = (typeof STEPS)[number]

export const STEP_LABELS: Record<Step, string> = {
  welcome: 'Welcome',
  database: 'Database',
  models: 'Models',
  review: 'Review & apply',
  connect: 'Connect',
  done: 'Done',
}

/** What the server told us before the wizard started. */
export interface WizardEnv {
  status: SetupStatus | null
  detect: DetectResponse | null
  models: ModelsResponse | null
}

export function envLockedSet(env: WizardEnv): Set<string> {
  return new Set(env.status?.env_locked || [])
}

/** The store is fixed by the environment (and reachable): nothing to choose. */
export function storeFromEnv(env: WizardEnv): boolean {
  return !!env.detect?.store.from_env || envLockedSet(env).has('vector_store')
}

export function voyageKeyAvailable(env: WizardEnv, cfg: DraftConfig): boolean {
  return !!cfg.voyageAPIKey.trim() || !!env.detect?.voyage_key_from_env || !!env.detect?.voyage_key_set
}

/** Everything needed is already provided by the environment. */
export function fullyFromEnv(env: WizardEnv): boolean {
  const d = env.detect
  return !!d && d.store.ok && storeFromEnv(env) && d.voyage_key_from_env
}

/** Pre-fill a draft from the detection result. */
export function prefill(cfg: DraftConfig, env: WizardEnv): DraftConfig {
  const d = env.detect
  if (!d) return cfg
  const s = d.suggested
  return {
    ...cfg,
    prefilled: true,
    vectorStore: storeFromEnv(env) ? d.store.type : s.vector_store || cfg.vectorStore,
    embedder: s.embedder || 'voyage',
    voyageModel: s.voyage_model || cfg.voyageModel,
    voyageDim: s.voyage_dim || cfg.voyageDim,
    rerankerModel: s.reranker_model ?? cfg.rerankerModel,
    sqlitePath: cfg.sqlitePath || d.sqlite_store?.path || '',
  }
}

/**
 * Convert the draft into the flat JSON the backend expects. Settings fixed by
 * environment variables are left out (except vector_store / embedder, which
 * the endpoint requires), and empty secrets are omitted so saved or
 * environment keys stay in place.
 */
export function draftToRequest(cfg: DraftConfig, env: WizardEnv): SetupApplyRequest {
  const locked = envLockedSet(env)
  const fromEnvStore = storeFromEnv(env)
  const keep = (key: string) => !locked.has(key)
  const req: SetupApplyRequest = {
    vector_store: fromEnvStore && env.detect ? env.detect.store.type : cfg.vectorStore,
    embedder: cfg.embedder,
  }
  if (!fromEnvStore) {
    if (cfg.vectorStore === 'pgvector' && cfg.pgvectorDSN.trim() && keep('pgvector_dsn')) req.pgvector_dsn = cfg.pgvectorDSN.trim()
    if (cfg.vectorStore === 'sqlite' && cfg.sqlitePath.trim() && keep('sqlite_path')) req.sqlite_path = cfg.sqlitePath.trim()
  }
  if (cfg.embedder === 'voyage') {
    if (cfg.voyageAPIKey.trim() && keep('voyage_api_key')) req.voyage_api_key = cfg.voyageAPIKey.trim()
    if (keep('voyage_model')) req.voyage_model = cfg.voyageModel
    if (keep('voyage_dim')) req.voyage_dim = cfg.voyageDim
  }
  if (cfg.embedder === 'openai') {
    if (cfg.openaiAPIKey.trim()) req.openai_api_key = cfg.openaiAPIKey.trim()
    req.openai_model = cfg.openaiModel
    req.openai_base_url = cfg.openaiBaseURL
    req.openai_dim = cfg.openaiDim
  }
  if (cfg.embedder === 'tei') req.tei_url = cfg.teiURL
  // The reranker is a Voyage model; it works with any embedder as long as a Voyage key exists.
  if (keep('reranker_model')) req.reranker_model = cfg.embedder === 'voyage' || voyageKeyAvailable(env, cfg) ? cfg.rerankerModel : ''
  return req
}

export interface StepProps {
  cfg: DraftConfig
  updateCfg: (patch: Partial<DraftConfig>) => void
  env: WizardEnv
  onBack: () => void
  onNext: () => void
}
