import { LayoutGrid, List, Upload, Plug, BookOpen, Settings as SettingsIcon, X } from 'lucide-react'
import { PAGE_LABELS, type Page } from '../App'
import type { ProjectSummary } from '../lib/api'
import { formatCompact } from '../lib/format'
import { useServerStatus } from '../lib/server'
import { Brand } from './Login'

interface SidebarProps {
  page: Page
  onNavigate: (p: Page) => void
  projects: ProjectSummary[]
  activeProject: string
  onSelectProject: (id: string) => void
  /** Drawer state on narrow screens. */
  open: boolean
  onClose: () => void
}

const navItems: { page: Page; icon: typeof LayoutGrid }[] = [
  { page: 'overview', icon: LayoutGrid },
  { page: 'chunks', icon: List },
  { page: 'import', icon: Upload },
  { page: 'connect', icon: Plug },
  { page: 'docs', icon: BookOpen },
  { page: 'settings', icon: SettingsIcon },
]

export function Sidebar({ page, onNavigate, projects, activeProject, onSelectProject, open, onClose }: SidebarProps) {
  const { health } = useServerStatus()
  return (
    <>
      {open && <div className="nav-backdrop" onClick={onClose} />}
      <aside className={`sidebar ${open ? 'open' : ''}`}>
        <div className="sidebar-top">
          <Brand />
          <button className="icon-btn nav-close" onClick={onClose} title="Close menu">
            <X size={15} />
          </button>
        </div>

        <nav>
          {navItems.map((item) => {
            const Icon = item.icon
            return (
              <button
                type="button"
                key={item.page}
                className={`nav-item ${page === item.page ? 'active' : ''}`}
                onClick={() => onNavigate(item.page)}
              >
                <Icon size={15} strokeWidth={1.6} />
                {PAGE_LABELS[item.page]}
              </button>
            )
          })}
        </nav>

        <div className="nav-label">Projects</div>
        <div className="proj-list">
          {projects.length === 0 ? (
            <div className="proj-empty">
              No projects yet.{' '}
              <button type="button" className="link-btn inline" onClick={() => onNavigate('connect')}>Connect a machine</button>
            </div>
          ) : (
            projects.map((p) => (
              <button
                type="button"
                key={p.project_id}
                className={`proj ${activeProject === p.project_id ? 'active' : ''}`}
                onClick={() => onSelectProject(p.project_id)}
                title={`${p.project_id} — ${p.chunk_count} chunks`}
              >
                <span
                  className="status-dot"
                  style={{ background: p.sources.some((s) => s.last_sync) ? 'var(--good)' : 'var(--text-faint)' }}
                />
                <span className="proj-name mono">{p.project_id}</span>
                <span className="count tnum">{formatCompact(p.chunk_count)}</span>
              </button>
            ))
          )}
        </div>

        {health && (
          <div className="sidebar-foot mono">
            enowx-rag {health.version}
          </div>
        )}
      </aside>
    </>
  )
}
