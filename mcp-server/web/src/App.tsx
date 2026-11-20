import { useState, useCallback, useEffect, useRef } from 'react'
import { Loader2, RotateCcw, WifiOff } from 'lucide-react'
import { Sidebar } from './components/Sidebar'
import { Topbar } from './components/Topbar'
import { Login, Brand } from './components/Login'
import { Overview } from './pages/Overview'
import { Chunks } from './pages/Chunks'
import { Import } from './pages/Import'
import { Connect } from './pages/Connect'
import { Docs } from './pages/Docs'
import { Settings } from './pages/Settings'
import { Wizard } from './pages/onboarding/Wizard'
import { useTheme } from './lib/useTheme'
import { api, ApiError, clearToken, getToken, UNAUTHORIZED_EVENT, type ProjectSummary } from './lib/api'
import { EventsProvider, useEventListener } from './lib/sse'
import { ServerStatusProvider } from './lib/server'

export type Page = 'overview' | 'chunks' | 'import' | 'connect' | 'docs' | 'settings'

export const PAGES: Page[] = ['overview', 'chunks', 'import', 'connect', 'docs', 'settings']

export const PAGE_LABELS: Record<Page, string> = {
  overview: 'Overview',
  chunks: 'Chunks',
  import: 'Import',
  connect: 'Connect',
  docs: 'Docs',
  settings: 'Settings',
}

type AppState = 'checking' | 'offline' | 'login' | 'wizard' | 'dashboard'

function App() {
  const { theme, toggleTheme } = useTheme()
  const [appState, setAppState] = useState<AppState>('checking')
  const [loginReason, setLoginReason] = useState('')
  const stateRef = useRef(appState)
  stateRef.current = appState

  // Decide what to show: login, onboarding wizard or the dashboard.
  const boot = useCallback(async () => {
    setAppState('checking')
    let authRequired = false
    let configured = true
    try {
      const [health, status] = await Promise.all([api.health(), api.setupStatus()])
      authRequired = health.auth_required || status.auth_required
      configured = status.configured
    } catch {
      setAppState('offline')
      return
    }
    if (authRequired && !getToken()) {
      setLoginReason('')
      setAppState('login')
      return
    }
    if (authRequired) {
      try {
        await api.validateToken(getToken())
      } catch (e) {
        if (e instanceof ApiError && (e.status === 401 || e.status === 403)) {
          clearToken()
          setLoginReason('Your saved token is no longer accepted. Sign in again.')
          setAppState('login')
          return
        }
        // Other failures (store down, …) still let the dashboard show what it can.
      }
    }
    setAppState(configured ? 'dashboard' : 'wizard')
  }, [])

  useEffect(() => {
    boot()
  }, [boot])

  // Any API call answering 401 brings the login screen back.
  useEffect(() => {
    const onUnauthorized = () => {
      if (stateRef.current === 'login') return
      clearToken()
      setLoginReason('Your session token was rejected. Sign in again.')
      setAppState('login')
    }
    window.addEventListener(UNAUTHORIZED_EVENT, onUnauthorized)
    return () => window.removeEventListener(UNAUTHORIZED_EVENT, onUnauthorized)
  }, [])

  // While offline, retry by itself.
  useEffect(() => {
    if (appState !== 'offline') return
    const t = window.setInterval(boot, 5000)
    return () => window.clearInterval(t)
  }, [appState, boot])

  const signOut = useCallback(() => {
    clearToken()
    setLoginReason('Signed out.')
    setAppState('login')
  }, [])

  if (appState === 'checking') {
    return (
      <div className="app-loading">
        <Loader2 size={16} className="spin" />
        <span>Connecting to the server…</span>
      </div>
    )
  }

  if (appState === 'offline') {
    return (
      <div className="login-shell">
        <div className="login-card">
          <Brand />
          <h1><WifiOff size={18} style={{ verticalAlign: '-3px', marginRight: 8 }} />Can't reach the server</h1>
          <p className="login-sub">
            <span className="mono">{window.location.origin}/api/health</span> did not answer. The server may be restarting or stopped.
            Retrying every 5 seconds…
          </p>
          <button className="btn primary login-btn" onClick={boot}>
            <RotateCcw size={14} /> Retry now
          </button>
        </div>
      </div>
    )
  }

  if (appState === 'login') {
    return <Login theme={theme} onToggleTheme={toggleTheme} onSuccess={boot} reason={loginReason} />
  }

  if (appState === 'wizard') {
    return (
      <Wizard
        onComplete={boot}
        onNeedLogin={() => {
          setLoginReason('Setup needs the admin token when you are not on the server itself.')
          setAppState('login')
        }}
        theme={theme}
        onToggleTheme={toggleTheme}
      />
    )
  }

  return (
    <ServerStatusProvider>
      <EventsProvider>
        <Dashboard
          theme={theme}
          onToggleTheme={toggleTheme}
          onSignOut={signOut}
          onRunWizard={() => setAppState('wizard')}
        />
      </EventsProvider>
    </ServerStatusProvider>
  )
}

function pageFromHash(): Page {
  const h = window.location.hash.replace(/^#\/?/, '').split('?')[0] as Page
  return PAGES.includes(h) ? h : 'overview'
}

const PROJECT_EVENTS = [
  'index_completed',
  'project_created',
  'project_deleted',
  'points_deleted',
  'documents_indexed',
  'job_completed',
  'sync_reported',
]

interface DashboardProps {
  theme: 'light' | 'dark'
  onToggleTheme: () => void
  onSignOut: () => void
  onRunWizard: () => void
}

function Dashboard({ theme, onToggleTheme, onSignOut, onRunWizard }: DashboardProps) {
  const [page, setPage] = useState<Page>(pageFromHash)
  const [projects, setProjects] = useState<ProjectSummary[] | null>(null)
  const [activeProject, setActiveProject] = useState<string>(() => {
    try {
      return localStorage.getItem('enowx-active-project') || ''
    } catch {
      return ''
    }
  })
  const [navOpen, setNavOpen] = useState(false)

  const refreshProjects = useCallback(() => {
    api.listProjects()
      .then((list) => setProjects(Array.isArray(list) ? list : []))
      .catch(() => setProjects((prev) => prev ?? []))
  }, [])

  useEffect(() => {
    refreshProjects()
    const t = window.setInterval(refreshProjects, 30000)
    return () => window.clearInterval(t)
  }, [refreshProjects])

  // Debounced refresh when projects or chunk counts change.
  const debounce = useRef<number | undefined>(undefined)
  useEventListener(() => {
    window.clearTimeout(debounce.current)
    debounce.current = window.setTimeout(refreshProjects, 600)
  }, PROJECT_EVENTS)

  // Keep the active project valid.
  useEffect(() => {
    if (!projects) return
    if (projects.length === 0) {
      if (activeProject) setActiveProject('')
      return
    }
    if (!projects.find((p) => p.project_id === activeProject)) setActiveProject(projects[0].project_id)
  }, [projects, activeProject])

  useEffect(() => {
    try {
      if (activeProject) localStorage.setItem('enowx-active-project', activeProject)
    } catch {
      // ignore
    }
  }, [activeProject])

  useEffect(() => {
    const onHash = () => setPage(pageFromHash())
    window.addEventListener('hashchange', onHash)
    return () => window.removeEventListener('hashchange', onHash)
  }, [])

  const navigate = useCallback((p: Page) => {
    setPage(p)
    setNavOpen(false)
    if (pageFromHash() !== p) window.location.hash = `/${p}`
  }, [])

  // Start each page at the top.
  useEffect(() => {
    document.querySelector('.content')?.scrollTo(0, 0)
  }, [page])

  const openProject = useCallback((id: string) => {
    setActiveProject(id)
    navigate('chunks')
  }, [navigate])

  return (
    <div className="app">
      <Sidebar
        page={page}
        onNavigate={navigate}
        projects={projects ?? []}
        activeProject={activeProject}
        onSelectProject={openProject}
        open={navOpen}
        onClose={() => setNavOpen(false)}
      />
      <div className="main">
        <Topbar
          theme={theme}
          onToggleTheme={onToggleTheme}
          activeProject={activeProject}
          page={page}
          onSignOut={onSignOut}
          onOpenNav={() => setNavOpen(true)}
        />
        <div className="content">
          {page === 'overview' && (
            <Overview projects={projects} onRefreshProjects={refreshProjects} onNavigate={navigate} onOpenProject={openProject} />
          )}
          {page === 'chunks' && (
            <Chunks
              projects={projects}
              activeProject={activeProject}
              onSelectProject={setActiveProject}
              onNavigate={navigate}
            />
          )}
          {page === 'import' && <Import onNavigate={navigate} />}
          {page === 'connect' && <Connect />}
          {page === 'docs' && <Docs />}
          {page === 'settings' && <Settings onRunWizard={onRunWizard} />}
        </div>
      </div>
    </div>
  )
}

export default App
