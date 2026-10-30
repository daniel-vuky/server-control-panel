import { useCallback, useEffect, useRef, useState } from 'react'
import type { Container, LogEnd, LogLine, Project } from '../api/types.gen'
import { backoffDelay, useLive } from '../app/live'
import { isNotCreated } from './containers'
import { detectLevel, type LogLevel } from './logLevel'

/** MAX_LINES is how many lines the view holds; the oldest drop first. */
export const MAX_LINES = 2000

/** HeldLine is a line as the view holds it: an id for React and its level, read once. */
export type HeldLine = LogLine & { id: number; level: LogLevel | null }

/**
 * LogStreamState is how the stream stands: live (open, or reopening after a
 * slow or dropped connection), notLive (it ended with nothing running) or
 * ended (the containers stopped after live lines), and the refusals: tooMany
 * (waits for Retry), dockerDown, missing (the folder is gone) and error
 * (message says why; waits for Retry).
 */
export type LogStreamState =
  | 'live'
  | 'ended'
  | 'notLive'
  | 'tooMany'
  | 'dockerDown'
  | 'missing'
  | 'error'

type Options = {
  /** onBacklog gets the lines held after the first backlog of a selection. */
  onBacklog?: (lines: HeldLine[]) => void
  /** onUnknownService is called when the server does not know the service; pick All. */
  onUnknownService?: () => void
}

/** What the stream waits for before it reopens on its own. */
type Wait = 'active' | 'dockerUp' | null

/** selectionContainers are the containers of the selection (all for ""), without Not created rows. */
export function selectionContainers(project: Project, service: string): Container[] {
  return project.containers.filter(
    (c) => !isNotCreated(c) && (service === '' || c.service === service),
  )
}

const isActive = (c: Container) => c.state === 'running' || c.state === 'restarting'

const lineKey = (l: LogLine) => `${l.container}\u0000${l.time}\u0000${l.text}`

function parseLines(data: unknown): LogLine[] | null {
  if (typeof data !== 'string') return null
  try {
    const v = JSON.parse(data) as { lines?: unknown } | null
    if (!v || !Array.isArray(v.lines)) return null
    return v.lines.filter(
      (l): l is LogLine =>
        typeof l === 'object' &&
        l !== null &&
        typeof l.container === 'string' &&
        typeof l.service === 'string' &&
        typeof l.time === 'string' &&
        typeof l.text === 'string',
    )
  } catch {
    return null
  }
}

function parseEnd(data: unknown): LogEnd {
  if (typeof data === 'string') {
    try {
      const v = JSON.parse(data) as Partial<LogEnd> | null
      if (v && typeof v.reason === 'string') {
        return { reason: v.reason, message: typeof v.message === 'string' ? v.message : '' }
      }
    } catch {
      // An unreadable end is an error end.
    }
  }
  return { reason: 'error', message: '' }
}

/**
 * newPart is the part of a reopened stream's backlog that is not held yet:
 * lines not already held and not older than the newest held line.
 */
function newPart(held: HeldLine[], backlog: LogLine[]): LogLine[] {
  if (held.length === 0) return backlog
  const keys = new Set(held.map(lineKey))
  const newest = Date.parse(held[held.length - 1].time)
  return backlog.filter((l) => !keys.has(lineKey(l)) && !(Date.parse(l.time) < newest))
}

/**
 * useLogStream follows GET /api/projects/{id}/logs for one selection (a
 * service, or "" for All). It holds the newest MAX_LINES lines and reopens on
 * its own: after `exited` when a container of the selection runs or restarts
 * again, after `docker_down` when Docker is up again, and after `slow` or a
 * network error with the live state's backoff, keeping the lines held. A new
 * selection clears the lines. `too_many_log_streams` and `error` wait for
 * retry(). newCount counts the lines added after the selection's first
 * backlog (it keeps counting past the cap).
 */
export function useLogStream(project: Project, service: string, opts: Options = {}) {
  const { dockerUp } = useLive()
  const [lines, setLines] = useState<HeldLine[]>([])
  const [state, setState] = useState<LogStreamState>('live')
  const [message, setMessage] = useState('')
  const [newCount, setNewCount] = useState(0)
  const [wait, setWait] = useState<Wait>(null)
  // opens counts the sources opened for this selection; a bump opens the next one.
  const [opens, setOpens] = useState(0)

  const held = useRef<HeldLine[]>([])
  const nextId = useRef(0)
  // retries counts reconnects (slow, network error) since a source last sent lines.
  const retries = useRef(0)
  // waitRetries counts reopens in a row after an end whose wait was already
  // true (a live state a tick behind); live lines reset it.
  const waitRetries = useRef(0)
  // backlogSeen: the selection's first backlog arrived.
  const backlogSeen = useRef(false)
  // sawFalse: since the stream ended, the wait condition was false once.
  const sawFalse = useRef(false)
  const selection = useRef<string | null>(null)
  const optsRef = useRef(opts)
  useEffect(() => {
    optsRef.current = opts
  })

  const projectId = project.id

  // biome-ignore lint/correctness/useExhaustiveDependencies: opens re-runs it to reopen the stream.
  useEffect(() => {
    if (typeof EventSource === 'undefined') return
    const key = `${projectId}|${service}`
    if (selection.current !== key) {
      selection.current = key
      held.current = []
      backlogSeen.current = false
      retries.current = 0
      waitRetries.current = 0
      setLines([])
      setNewCount(0)
      setState('live')
      setMessage('')
      setWait(null)
    }

    const query = service === '' ? '' : `?service=${encodeURIComponent(service)}`
    const es = new EventSource(`/api/projects/${projectId}/logs${query}`)
    let done = false
    let backlog = true
    let liveSeen = false
    let timer: ReturnType<typeof setTimeout> | undefined

    const finish = () => {
      done = true
      es.close()
    }
    const reconnect = () => {
      finish()
      retries.current++
      timer = setTimeout(() => setOpens((n) => n + 1), backoffDelay(retries.current))
    }
    const waitFor = (w: Exclude<Wait, null>) => {
      sawFalse.current = false
      setWait(w)
    }

    es.addEventListener('lines', (e: Event) => {
      if (done) return
      const batch = parseLines((e as MessageEvent).data)
      if (!batch) return
      const isBacklog = backlog
      backlog = false
      retries.current = 0
      if (!isBacklog) {
        liveSeen = true
        waitRetries.current = 0
      }
      const add = (isBacklog ? newPart(held.current, batch) : batch).map(
        (l): HeldLine => ({ ...l, id: nextId.current++, level: detectLevel(l.text) }),
      )
      const all = held.current.concat(add)
      held.current = all.length > MAX_LINES ? all.slice(all.length - MAX_LINES) : all
      setLines(held.current)
      if (backlogSeen.current) setNewCount((n) => n + add.length)
      setState('live')
      setMessage('')
      if (!backlogSeen.current) {
        backlogSeen.current = true
        optsRef.current.onBacklog?.(held.current)
      }
    })

    es.addEventListener('end', (e: Event) => {
      if (done) return
      const end = parseEnd((e as MessageEvent).data)
      if (end.reason === 'slow') {
        reconnect()
        return
      }
      finish()
      switch (end.reason) {
        case 'exited':
          setState(liveSeen ? 'ended' : 'notLive')
          waitFor('active')
          return
        case 'docker_down':
          setState('dockerDown')
          waitFor('dockerUp')
          return
        case 'too_many_log_streams':
          setState('tooMany')
          return
        case 'not_found':
          setState('missing')
          return
        case 'unknown_service':
          if (service !== '' && optsRef.current.onUnknownService) {
            optsRef.current.onUnknownService()
            return
          }
          break
      }
      setState('error')
      setMessage(end.message)
    })

    // A network error or the server closing without an end: reopen with the backoff.
    es.addEventListener('error', () => {
      if (!done) reconnect()
    })

    return () => {
      finish()
      clearTimeout(timer)
    }
  }, [projectId, service, opens])

  const active = selectionContainers(project, service).some(isActive)

  // Reopen once what the stream waits for is true. When it was already true
  // as the stream ended (a live state a tick behind), retry with the backoff.
  useEffect(() => {
    if (wait === null) return
    const ok = wait === 'active' ? active : dockerUp === true
    if (!ok) {
      sawFalse.current = true
      return
    }
    const reopen = () => {
      setWait(null)
      setOpens((n) => n + 1)
    }
    if (sawFalse.current) {
      waitRetries.current = 0
      reopen()
      return
    }
    waitRetries.current++
    const t = setTimeout(reopen, backoffDelay(waitRetries.current))
    return () => clearTimeout(t)
  }, [wait, active, dockerUp])

  const retry = useCallback(() => {
    retries.current = 0
    waitRetries.current = 0
    setState('live')
    setMessage('')
    setWait(null)
    setOpens((n) => n + 1)
  }, [])

  return { lines, state, message, newCount, retry }
}
