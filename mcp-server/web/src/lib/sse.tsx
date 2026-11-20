// Live events: one authenticated SSE connection shared app-wide.
//
// EventSource cannot send an Authorization header, so the stream is read with
// fetch() + ReadableStream and parsed by hand (event:/data: frames, ":"
// comments ignored). The list is seeded from GET /api/activity and merged on
// every (re)connect, so history survives reloads and dropped connections.
// Jobs (GET /api/jobs + job_* events) are tracked here too, so every page
// sees the same progress.

import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { api, authHeaders, getToken, UNAUTHORIZED_EVENT, type Event, type Job } from './api'

export type SSEEvent = Event

export interface UseEventsResult {
  events: SSEEvent[]
  connected: boolean
  clear: () => void
}

interface EventsContextValue extends UseEventsResult {
  jobs: Job[]
  trackJob: (job: Job) => void
  refreshJobs: () => void
  subscribe: (fn: (ev: SSEEvent) => void) => () => void
}

const EventsContext = createContext<EventsContextValue | null>(null)

const MAX_EVENTS = 100

function eventKey(e: SSEEvent): string {
  return `${e.type}|${e.timestamp}|${JSON.stringify(e.data ?? {})}`
}

function mergeEvents(a: SSEEvent[], b: SSEEvent[]): SSEEvent[] {
  const seen = new Set<string>()
  const out: SSEEvent[] = []
  for (const e of [...a, ...b]) {
    const k = eventKey(e)
    if (seen.has(k)) continue
    seen.add(k)
    out.push(e)
  }
  out.sort((x, y) => new Date(y.timestamp).getTime() - new Date(x.timestamp).getTime())
  return out.slice(0, MAX_EVENTS)
}

function upsertJob(list: Job[], job: Job): Job[] {
  const i = list.findIndex((j) => j.id === job.id)
  if (i === -1) return [job, ...list].slice(0, 50)
  const next = list.slice()
  next[i] = { ...list[i], ...job }
  return next
}

/** Parse one SSE frame ("event: x\ndata: {...}") into an Event. */
function parseFrame(frame: string): SSEEvent | null {
  let type = ''
  const data: string[] = []
  for (const raw of frame.split(/\r?\n/)) {
    if (!raw || raw.startsWith(':')) continue
    const idx = raw.indexOf(':')
    const field = idx === -1 ? raw : raw.slice(0, idx)
    let value = idx === -1 ? '' : raw.slice(idx + 1)
    if (value.startsWith(' ')) value = value.slice(1)
    if (field === 'event') type = value
    else if (field === 'data') data.push(value)
  }
  if (data.length === 0) return null
  try {
    const parsed = JSON.parse(data.join('\n')) as Partial<SSEEvent> & Record<string, unknown>
    const isEnvelope = typeof parsed.type === 'string' || typeof parsed.timestamp === 'string'
    return {
      type: type || (typeof parsed.type === 'string' ? parsed.type : 'message'),
      timestamp: typeof parsed.timestamp === 'string' ? parsed.timestamp : new Date().toISOString(),
      data: isEnvelope ? (parsed.data as Record<string, unknown> | undefined) : parsed,
    }
  } catch {
    return null
  }
}

export function EventsProvider({ children }: { children: ReactNode }) {
  const [events, setEvents] = useState<SSEEvent[]>([])
  const [connected, setConnected] = useState(false)
  const [jobs, setJobs] = useState<Job[]>([])
  const listeners = useRef(new Set<(ev: SSEEvent) => void>())

  const refreshJobs = useCallback(() => {
    api.jobs().then((list) => {
      setJobs((prev) => {
        // Keep jobs we know about locally that the server list no longer has.
        let next = Array.isArray(list) ? list : []
        for (const j of prev) if (!next.find((x) => x.id === j.id)) next = [...next, j]
        return next
          .sort((a, b) => new Date(b.started_at).getTime() - new Date(a.started_at).getTime())
          .slice(0, 50)
      })
    }).catch(() => {})
  }, [])

  const seedActivity = useCallback(() => {
    api.activity(50).then((list) => {
      if (Array.isArray(list)) setEvents((prev) => mergeEvents(prev, list))
    }).catch(() => {})
  }, [])

  const trackJob = useCallback((job: Job) => setJobs((prev) => upsertJob(prev, job)), [])

  // The SSE connection with reconnect + exponential backoff.
  useEffect(() => {
    let stopped = false
    let retry = 0
    let timer: number | undefined
    let ctrl: AbortController | null = null

    const deliver = (ev: SSEEvent) => {
      setEvents((prev) => mergeEvents([ev], prev))
      if (ev.type.startsWith('job_') && ev.data && typeof ev.data.id === 'string') {
        setJobs((prev) => upsertJob(prev, ev.data as unknown as Job))
      }
      listeners.current.forEach((fn) => {
        try {
          fn(ev)
        } catch {
          // a listener must not break the stream
        }
      })
    }

    const schedule = () => {
      if (stopped) return
      const delay = Math.min(30000, 1000 * 2 ** retry) + Math.random() * 400
      retry++
      timer = window.setTimeout(connect, delay)
    }

    const connect = async () => {
      if (stopped) return
      ctrl = new AbortController()
      try {
        const resp = await fetch('/api/events', {
          headers: { Accept: 'text/event-stream', ...authHeaders(getToken()) },
          signal: ctrl.signal,
          cache: 'no-store',
        })
        if (resp.status === 401) {
          setConnected(false)
          window.dispatchEvent(new CustomEvent(UNAUTHORIZED_EVENT))
          return // the app re-mounts the provider after a new login
        }
        if (!resp.ok || !resp.body) throw new Error(`HTTP ${resp.status}`)
        setConnected(true)
        retry = 0
        // Catch up on anything missed while disconnected.
        seedActivity()
        refreshJobs()

        const reader = resp.body.getReader()
        const decoder = new TextDecoder()
        let buf = ''
        for (;;) {
          const { value, done } = await reader.read()
          if (done) break
          buf += decoder.decode(value, { stream: true })
          let m: RegExpExecArray | null
          const sep = /\r?\n\r?\n/
          while ((m = sep.exec(buf)) !== null) {
            const frame = buf.slice(0, m.index)
            buf = buf.slice(m.index + m[0].length)
            const ev = parseFrame(frame)
            if (ev) deliver(ev)
          }
        }
        throw new Error('stream closed')
      } catch {
        if (stopped) return
        setConnected(false)
        schedule()
      }
    }

    seedActivity()
    refreshJobs()
    connect()

    return () => {
      stopped = true
      if (timer) window.clearTimeout(timer)
      ctrl?.abort()
      setConnected(false)
    }
  }, [seedActivity, refreshJobs])

  // Fallback polling while a job runs: fast when the stream is down.
  const anyRunning = jobs.some((j) => j.state === 'running')
  useEffect(() => {
    if (!anyRunning) return
    const t = window.setInterval(refreshJobs, connected ? 10000 : 2500)
    return () => window.clearInterval(t)
  }, [anyRunning, connected, refreshJobs])

  const subscribe = useCallback((fn: (ev: SSEEvent) => void) => {
    listeners.current.add(fn)
    return () => {
      listeners.current.delete(fn)
    }
  }, [])

  const clear = useCallback(() => setEvents([]), [])

  const value = useMemo<EventsContextValue>(
    () => ({ events, connected, clear, jobs, trackJob, refreshJobs, subscribe }),
    [events, connected, clear, jobs, trackJob, refreshJobs, subscribe],
  )

  return <EventsContext.Provider value={value}>{children}</EventsContext.Provider>
}

function useEventsContext(): EventsContextValue {
  const ctx = useContext(EventsContext)
  if (!ctx) throw new Error('useEvents must be used inside <EventsProvider>')
  return ctx
}

/** Event history (newest first) + live connection state. */
export function useEvents(maxEvents: number = 50): UseEventsResult {
  const { events, connected, clear } = useEventsContext()
  return { events: events.slice(0, maxEvents), connected, clear }
}

/** All known jobs (newest first) and helpers to track new ones. */
export function useJobs() {
  const { jobs, trackJob, refreshJobs } = useEventsContext()
  return { jobs, trackJob, refreshJobs }
}

/** One job by id (undefined until known). */
export function useJob(id: string | undefined): Job | undefined {
  const { jobs } = useEventsContext()
  return id ? jobs.find((j) => j.id === id) : undefined
}

/**
 * Call `fn` for every live event whose type is in `types` (all when omitted).
 * Unlike watching `events[0]`, this never misses events that arrive in bursts.
 */
export function useEventListener(fn: (ev: SSEEvent) => void, types?: string[]) {
  const { subscribe } = useEventsContext()
  const fnRef = useRef(fn)
  fnRef.current = fn
  const key = types ? types.join(',') : ''
  useEffect(() => {
    const allow = key ? new Set(key.split(',')) : null
    return subscribe((ev) => {
      if (!allow || allow.has(ev.type)) fnRef.current(ev)
    })
  }, [subscribe, key])
}
