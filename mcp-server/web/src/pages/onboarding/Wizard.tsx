import { useState, useCallback, useEffect, useRef } from 'react'
import { Sun, Moon, Loader2 } from 'lucide-react'
import { api, ApiError, errorText, isAuthError, type SetupApplyRequest } from '../../lib/api'
import { Brand } from '../../components/Login'
import { RestartOverlay } from '../../components/RestartOverlay'
import { ErrorBox } from '../../components/ui'
import { STEPS, STEP_LABELS, SECRET_FIELDS, defaultDraft, prefill, type Step, type DraftConfig, type WizardEnv } from './types'
import { StepWelcome } from './StepWelcome'
import { StepDatabase } from './StepDatabase'
import { StepModels } from './StepModels'
import { StepReview } from './StepReview'
import { StepConnect } from './StepConnect'
import { StepDone } from './StepDone'

interface WizardProps {
  onComplete: () => void
  /** Setup endpoints answered 401/403: the admin token is needed first. */
  onNeedLogin: () => void
  theme: 'light' | 'dark'
  onToggleTheme: () => void
}

export interface ApplyOutcome {
  restarted: boolean
  /** Saved, but the server could not restart itself. */
  manualRestart: boolean
}

export function Wizard({ onComplete, onNeedLogin, theme, onToggleTheme }: WizardProps) {
  const [step, setStep] = useState<Step>(loadStep)
  const [cfg, setCfg] = useState<DraftConfig>(loadDraft)
  const [env, setEnv] = useState<WizardEnv>({ status: null, detect: null, models: null })
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [restart, setRestart] = useState<{ prevBootId: string } | null>(null)
  const [outcome, setOutcome] = useState<ApplyOutcome | null>(null)
  const needLogin = useRef(onNeedLogin)
  needLogin.current = onNeedLogin

  // Detect what the server already knows and pre-fill the draft.
  useEffect(() => {
    let cancelled = false
    ;(async () => {
      try {
        const status = await api.setupStatus()
        const detect = await api.detect()
        const models = await api.models().catch((e) => {
          if (isAuthError(e)) throw e
          return null
        })
        if (cancelled) return
        const next = { status, detect, models }
        setEnv(next)
        setCfg((c) => (c.prefilled ? c : prefill(c, next)))
      } catch (e) {
        if (cancelled) return
        if (isAuthError(e)) {
          needLogin.current()
          return
        }
        setLoadError(errorText(e, 'Could not ask the server what it already has.'))
      } finally {
        if (!cancelled) setLoading(false)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [])

  // Persist the draft (without secrets) and the step.
  useEffect(() => {
    try {
      const safe: Partial<DraftConfig> = { ...cfg }
      for (const k of SECRET_FIELDS) delete safe[k]
      localStorage.setItem('wizard-draft', JSON.stringify(safe))
    } catch {
      // ignore
    }
  }, [cfg])

  useEffect(() => {
    try {
      localStorage.setItem('wizard-step', step)
    } catch {
      // ignore
    }
  }, [step])

  const stepIndex = STEPS.indexOf(step)
  const go = useCallback((s: Step) => {
    setStep(s)
    window.scrollTo({ top: 0 })
  }, [])
  const next = useCallback(() => {
    if (stepIndex < STEPS.length - 1) go(STEPS[stepIndex + 1])
  }, [stepIndex, go])
  const back = useCallback(() => {
    if (stepIndex > 0) go(STEPS[stepIndex - 1])
  }, [stepIndex, go])

  const updateCfg = useCallback((patch: Partial<DraftConfig>) => {
    setCfg((prev) => ({ ...prev, ...patch }))
  }, [])

  /** POST /api/setup/apply (with restart when supported), then wait for the new process. */
  const apply = useCallback(async (req: SetupApplyRequest) => {
    let prevBootId = ''
    try {
      prevBootId = (await api.health()).boot_id
    } catch {
      // ignore: the restart wait falls back to "any boot id"
    }
    const canRestart = env.status?.restart_supported !== false
    try {
      const res = await api.setupApply({ ...req, restart: canRestart })
      if (res.restarting) {
        setRestart({ prevBootId })
      } else {
        setOutcome({ restarted: false, manualRestart: true })
        go('connect')
      }
    } catch (e) {
      if (e instanceof ApiError && e.status === 403) needLogin.current()
      throw e
    }
  }, [env.status, go])

  const finish = useCallback(() => {
    try {
      localStorage.removeItem('wizard-draft')
      localStorage.removeItem('wizard-step')
    } catch {
      // ignore
    }
    onComplete()
  }, [onComplete])

  const stepProps = { cfg, updateCfg, env, onBack: back, onNext: next }

  return (
    <div className="wizard-shell">
      <div className="wizard-topbar">
        <Brand />
        <span className="wizard-subtitle">Server setup</span>
        <div className="topbar-spacer" />
        <button className="icon-btn" onClick={onToggleTheme} title="Toggle theme">
          {theme === 'dark' ? <Sun size={15} strokeWidth={1.7} /> : <Moon size={15} strokeWidth={1.7} />}
        </button>
      </div>

      <div className="wizard-container">
        <div className="stepper">
          {STEPS.map((s, i) => (
            <div key={s} className={`step-group ${i === stepIndex ? 'current' : ''} ${i < stepIndex ? 'completed' : ''}`}>
              {i > 0 && <div className={`step-connector ${i <= stepIndex ? 'completed' : ''}`} />}
              <div className="step-item">
                <div className="step-circle">{i + 1}</div>
                <span className="step-label">{STEP_LABELS[s]}</span>
              </div>
            </div>
          ))}
        </div>

        {loading ? (
          <div className="card">
            <div className="card-body inline-loading"><Loader2 size={15} className="spin" /> Checking what this server already has…</div>
          </div>
        ) : (
          <>
            {loadError && <ErrorBox>{loadError} You can still configure everything by hand.</ErrorBox>}
            {step === 'welcome' && <StepWelcome {...stepProps} onApply={apply} />}
            {step === 'database' && <StepDatabase {...stepProps} />}
            {step === 'models' && <StepModels {...stepProps} />}
            {step === 'review' && <StepReview {...stepProps} onApply={apply} />}
            {step === 'connect' && <StepConnect {...stepProps} outcome={outcome} />}
            {step === 'done' && <StepDone {...stepProps} onFinish={finish} />}
          </>
        )}
      </div>

      {restart && (
        <RestartOverlay
          prevBootId={restart.prevBootId}
          title="Applying configuration"
          detail="Saving config.yaml and restarting the server with the new database and models."
          onDone={() => {
            setRestart(null)
            setOutcome({ restarted: true, manualRestart: false })
            go('connect')
          }}
          onDismiss={() => {
            setRestart(null)
            setOutcome({ restarted: false, manualRestart: true })
            go('connect')
          }}
        />
      )}
    </div>
  )
}

function loadStep(): Step {
  try {
    const stored = localStorage.getItem('wizard-step')
    if (stored && (STEPS as readonly string[]).includes(stored)) return stored as Step
  } catch {
    // ignore
  }
  return 'welcome'
}

function loadDraft(): DraftConfig {
  try {
    const stored = localStorage.getItem('wizard-draft')
    if (stored) return { ...defaultDraft, ...JSON.parse(stored) }
  } catch {
    // ignore
  }
  return defaultDraft
}
