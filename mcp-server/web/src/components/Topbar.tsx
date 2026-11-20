import { LogOut, Menu, Moon, Sun } from 'lucide-react'
import { PAGE_LABELS, type Page } from '../App'
import { getToken } from '../lib/api'
import { formatDuration, formatVersion } from '../lib/format'
import { useServerStatus } from '../lib/server'
import { useEventListener, useEvents } from '../lib/sse'

interface TopbarProps {
  theme: 'light' | 'dark'
  onToggleTheme: () => void
  activeProject: string
  page: Page
  onSignOut: () => void
  onOpenNav: () => void
}

/** healthy / restarting / offline, plus whether the live event stream is up. */
function StatusPill() {
  const { state, health, markRestarting } = useServerStatus()
  const { connected } = useEvents(1)
  useEventListener(() => markRestarting(), ['server_restarting'])

  let tone = 'good'
  let label = 'healthy'
  let title = health ? `${formatVersion(health.version)} · up ${formatDuration(health.uptime_s)}` : ''
  if (state === 'restarting') {
    tone = 'warn'
    label = 'restarting'
    title = 'Waiting for the server to come back…'
  } else if (state === 'offline') {
    tone = 'crit'
    label = 'offline'
    title = '/api/health is not answering'
  } else if (state === 'checking') {
    tone = 'muted'
    label = 'checking'
  } else if (!connected) {
    tone = 'warn'
    label = 'reconnecting'
    title = 'The server answers, but the live event stream is reconnecting.'
  }
  return (
    <span className={`status-pill ${tone}`} title={title}>
      <span className={`pulse ${state === 'healthy' && connected ? 'live' : ''}`} />
      {label}
    </span>
  )
}

export function Topbar({ theme, onToggleTheme, activeProject, page, onSignOut, onOpenNav }: TopbarProps) {
  const { health } = useServerStatus()
  const canSignOut = !!getToken() || !!health?.auth_required
  return (
    <div className="topbar">
      <button className="icon-btn menu-btn" onClick={onOpenNav} title="Menu" aria-label="Open menu">
        <Menu size={15} />
      </button>
      <div className="crumb">
        <span className="crumb-host">{window.location.host}</span>
        <span className="sep">/</span>
        <b>{PAGE_LABELS[page]}</b>
        {page === 'chunks' && activeProject && (
          <>
            <span className="sep">/</span>
            <span className="mono crumb-proj">{activeProject}</span>
          </>
        )}
      </div>
      <div className="topbar-spacer" />
      <StatusPill />
      <button className="icon-btn" onClick={onToggleTheme} title="Toggle theme">
        {theme === 'dark' ? <Sun size={15} strokeWidth={1.7} /> : <Moon size={15} strokeWidth={1.7} />}
      </button>
      {canSignOut && (
        <button className="icon-btn" onClick={onSignOut} title="Sign out (forget the admin token in this browser)">
          <LogOut size={15} strokeWidth={1.7} />
        </button>
      )}
    </div>
  )
}
