// Server liveness: /api/health polling shared app-wide, plus the helper that
// waits for a restart to finish (boot_id changes on every process start).

import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { api, type Health } from './api'

export type ServerState = 'healthy' | 'restarting' | 'offline' | 'checking'

interface ServerStatusValue {
  health: Health | null
  state: ServerState
  /** Mark the server as restarting until a new boot_id shows up. */
  markRestarting: () => void
  /** Re-check /api/health now. */
  refresh: () => void
}

const ServerStatusContext = createContext<ServerStatusValue | null>(null)

export function ServerStatusProvider({ children }: { children: ReactNode }) {
  const [health, setHealth] = useState<Health | null>(null)
  const [offline, setOffline] = useState(false)
  const [restartFrom, setRestartFrom] = useState<string | null>(null)

  const refresh = useCallback(() => {
    const ctrl = new AbortController()
    const t = window.setTimeout(() => ctrl.abort(), 5000)
    api.health(ctrl.signal)
      .then((h) => {
        setHealth(h)
        setOffline(false)
        setRestartFrom((from) => (from !== null && h.boot_id !== from ? null : from))
      })
      .catch(() => setOffline(true))
      .finally(() => window.clearTimeout(t))
  }, [])

  useEffect(() => {
    refresh()
    const every = restartFrom !== null || offline ? 2000 : 15000
    const t = window.setInterval(refresh, every)
    return () => window.clearInterval(t)
  }, [refresh, restartFrom, offline])

  const markRestarting = useCallback(() => {
    setRestartFrom((from) => from ?? health?.boot_id ?? '')
  }, [health])

  // Give up on "restarting" after a minute: from then on it is plain offline/healthy.
  useEffect(() => {
    if (restartFrom === null) return
    const t = window.setTimeout(() => setRestartFrom(null), 60000)
    return () => window.clearTimeout(t)
  }, [restartFrom])

  const state: ServerState = restartFrom !== null ? 'restarting' : offline ? 'offline' : health ? 'healthy' : 'checking'

  const value = useMemo(() => ({ health, state, markRestarting, refresh }), [health, state, markRestarting, refresh])
  return <ServerStatusContext.Provider value={value}>{children}</ServerStatusContext.Provider>
}

export function useServerStatus(): ServerStatusValue {
  const ctx = useContext(ServerStatusContext)
  if (!ctx) throw new Error('useServerStatus must be used inside <ServerStatusProvider>')
  return ctx
}

/**
 * Poll /api/health until boot_id differs from `prevBootId` (the restart has
 * finished). Resolves with the new health, rejects after `timeoutMs`.
 */
export async function waitForRestart(
  prevBootId: string,
  opts: { timeoutMs?: number; signal?: AbortSignal; onTick?: (elapsedMs: number) => void } = {},
): Promise<Health> {
  const timeout = opts.timeoutMs ?? 45000
  const start = Date.now()
  // Give the old process a moment to go down before the first probe.
  await sleep(800, opts.signal)
  for (;;) {
    if (opts.signal?.aborted) throw new Error('aborted')
    const elapsed = Date.now() - start
    opts.onTick?.(elapsed)
    if (elapsed > timeout) throw new Error('The server did not come back in time.')
    try {
      const ctrl = new AbortController()
      const t = window.setTimeout(() => ctrl.abort(), 3000)
      const h = await api.health(ctrl.signal).finally(() => window.clearTimeout(t))
      if (h.boot_id && h.boot_id !== prevBootId) return h
    } catch {
      // still down: keep polling
    }
    await sleep(1000, opts.signal)
  }
}

function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const t = window.setTimeout(resolve, ms)
    signal?.addEventListener('abort', () => {
      window.clearTimeout(t)
      reject(new Error('aborted'))
    }, { once: true })
  })
}

/** Re-render every `ms` so relative times ("2m ago") stay fresh. */
export function useTick(ms = 30000): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const t = window.setInterval(() => setNow(Date.now()), ms)
    return () => window.clearInterval(t)
  }, [ms])
  return now
}
