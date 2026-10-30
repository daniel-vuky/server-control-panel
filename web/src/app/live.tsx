import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from 'react'
import { checkDocker } from '../api/projects'
import type { LiveState, Project } from '../api/types.gen'

/**
 * connection is the state of the live stream: connecting until the first state
 * arrives, open while states flow, lost after an error until the next state,
 * expired when the Access session ended (only a reload signs in again).
 */
export type Connection = 'connecting' | 'open' | 'lost' | 'expired'

type Live = {
  connection: Connection
  /** null until the first state event arrives. */
  dockerUp: boolean | null
  /** null until the first state event arrives. */
  projects: Project[] | null
  /** True while a Docker re-check is in flight. */
  checking: boolean
  recheckDocker(): Promise<void>
}

const LiveContext = createContext<Live | null>(null)

/** EventSource.CLOSED, spelled out so a test double without the constants works. */
const CLOSED = 2
/** Delays before recreating a closed source: 1 s, 2 s, 5 s, then every 10 s. */
const BACKOFF_MS = [1000, 2000, 5000, 10000]

/**
 * backoffDelay is the wait before reconnect attempt n (1-based): 1 s, 2 s,
 * 5 s, then 10 s. The live state and the log stream both reconnect with it.
 */
export function backoffDelay(attempt: number): number {
  return BACKOFF_MS[Math.min(Math.max(attempt, 1), BACKOFF_MS.length) - 1]
}
/** After this many closed attempts in a row, ask whether the session expired. */
const CHECK_SESSION_AFTER = 3

/**
 * sessionExpired asks /api/me without following redirects: Access answers an
 * expired session with a redirect to its login (opaque here) or a 401/403.
 * A network error is not an expiry; the reconnect goes on.
 */
async function sessionExpired(): Promise<boolean> {
  try {
    const r = await fetch('/api/me', { redirect: 'manual' })
    return r.type === 'opaqueredirect' || r.status === 401 || r.status === 403
  } catch {
    return false
  }
}

function parseState(data: unknown): LiveState | null {
  if (typeof data !== 'string') return null
  try {
    const v = JSON.parse(data) as Partial<LiveState> | null
    if (v && typeof v.dockerUp === 'boolean' && Array.isArray(v.projects)) return v as LiveState
  } catch {
    // A malformed event is dropped; the next one corrects the state.
  }
  return null
}

/** LiveProvider holds the one EventSource of the tab and the latest state it sent. */
export function LiveProvider({ children }: { children: ReactNode }) {
  const [dockerUp, setDockerUp] = useState<boolean | null>(null)
  const [projects, setProjects] = useState<Project[] | null>(null)
  const [checking, setChecking] = useState(false)
  const [connection, setConnection] = useState<Connection>('connecting')

  useEffect(() => {
    if (typeof EventSource === 'undefined') return
    let es: EventSource | null = null
    let timer: ReturnType<typeof setTimeout> | undefined
    let stopped = false
    // hadOpen: a state arrived once, so an error now means the values are stale.
    let hadOpen = false
    // closedInRow: sources that ended CLOSED since the last state.
    let closedInRow = 0

    const reconnect = async () => {
      if (closedInRow >= CHECK_SESSION_AFTER && (await sessionExpired())) {
        // Retrying cannot sign in again; Reload does.
        if (!stopped) setConnection('expired')
        return
      }
      if (stopped) return
      timer = setTimeout(connect, backoffDelay(closedInRow))
    }

    function connect() {
      const src = new EventSource('/api/events')
      es = src
      src.addEventListener('state', (e: Event) => {
        if (es !== src) return
        const next = parseState((e as MessageEvent).data)
        if (!next) return
        setDockerUp(next.dockerUp)
        setProjects(next.projects)
        hadOpen = true
        closedInRow = 0
        setConnection('open')
      })
      src.addEventListener('error', () => {
        if (es !== src || stopped) return
        if (hadOpen) setConnection('lost')
        // A CONNECTING source is retried by the browser; a CLOSED one never is.
        if (src.readyState !== CLOSED) return
        src.close()
        es = null
        closedInRow++
        void reconnect()
      })
    }

    connect()
    return () => {
      stopped = true
      clearTimeout(timer)
      es?.close()
    }
  }, [])

  const recheckDocker = useCallback(async () => {
    setChecking(true)
    try {
      const r = await checkDocker()
      // The server also broadcasts; this makes the banner answer at once.
      setDockerUp(r.dockerUp)
    } catch {
      // A failed check leaves dockerUp as it was; the next state event corrects it.
    } finally {
      setChecking(false)
    }
  }, [])

  const value = useMemo<Live>(
    () => ({
      connection,
      dockerUp,
      projects,
      checking,
      recheckDocker,
    }),
    [connection, dockerUp, projects, checking, recheckDocker],
  )
  return <LiveContext.Provider value={value}>{children}</LiveContext.Provider>
}

export function useLive(): Live {
  const ctx = useContext(LiveContext)
  if (!ctx) throw new Error('useLive must be used inside LiveProvider')
  return ctx
}
