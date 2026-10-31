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
import type { Backup, BackupBatch, DriveStatus, LiveState, Project } from '../api/types.gen'

/**
 * connection is the state of the live stream: connecting until the first state
 * arrives, open while states flow, lost after an error until the next state,
 * expired when the Access session ended (only a reload signs in again).
 */
export type Connection = 'connecting' | 'open' | 'lost' | 'expired'

/** Live is what useLive returns. */
export type Live = {
  connection: Connection
  /** null until the first state event arrives. */
  dockerUp: boolean | null
  /** null until the first state event arrives. */
  projects: Project[] | null
  /** The active backups, running first; empty until the first state event. */
  backups: Backup[]
  /** The latest Back up all, running or finished; null before the first or when the state lacks it. */
  batch: BackupBatch | null
  /** The Google Drive connection; null until the first state event. */
  drive: DriveStatus | null
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
/**
 * STALE_MS is how long a stream may stay silent before it counts as stalled.
 * The server sends a ping event every 25 s, so a live stream is never this quiet.
 */
export const STALE_MS = 60_000

export type Watchdog = {
  /** rearm restarts the countdown; call it on every event the stream sends. */
  rearm(): void
  /** clear stops the countdown for good. */
  clear(): void
}

/**
 * armWatchdog calls onStale once when STALE_MS pass with no rearm. A
 * half-open connection (the server frozen, TCP still open) sends no error,
 * so silence is the only sign of it.
 */
export function armWatchdog(onStale: () => void, ms = STALE_MS): Watchdog {
  let timer: ReturnType<typeof setTimeout> | undefined
  let cleared = false
  const rearm = () => {
    if (cleared) return
    clearTimeout(timer)
    timer = setTimeout(() => {
      cleared = true
      onStale()
    }, ms)
  }
  rearm()
  return {
    rearm,
    clear() {
      cleared = true
      clearTimeout(timer)
    },
  }
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

/** A parsed state event: the Drive status may be missing from the stream. */
type ParsedState = Omit<LiveState, 'drive'> & { drive: DriveStatus | null }

const isBatch = (v: unknown): v is BackupBatch =>
  typeof v === 'object' &&
  v !== null &&
  typeof (v as BackupBatch).id === 'number' &&
  typeof (v as BackupBatch).total === 'number' &&
  typeof (v as BackupBatch).done === 'number'

function parseState(data: unknown): ParsedState | null {
  if (typeof data !== 'string') return null
  try {
    const v = JSON.parse(data) as Partial<LiveState> | null
    if (v && typeof v.dockerUp === 'boolean' && Array.isArray(v.projects)) {
      // A server without the backup fields (or an older one) still parses.
      return {
        ...v,
        backups: v.backups ?? [],
        batch: isBatch(v.batch) ? v.batch : null,
        drive: v.drive ?? null,
      } as ParsedState
    }
  } catch {
    // A malformed event is dropped; the next one corrects the state.
  }
  return null
}

/** LiveProvider holds the one EventSource of the tab and the latest state it sent. */
export function LiveProvider({ children }: { children: ReactNode }) {
  const [dockerUp, setDockerUp] = useState<boolean | null>(null)
  const [projects, setProjects] = useState<Project[] | null>(null)
  const [backups, setBackups] = useState<Backup[]>([])
  const [batch, setBatch] = useState<BackupBatch | null>(null)
  const [drive, setDrive] = useState<DriveStatus | null>(null)
  const [checking, setChecking] = useState(false)
  const [connection, setConnection] = useState<Connection>('connecting')

  useEffect(() => {
    if (typeof EventSource === 'undefined') return
    let es: EventSource | null = null
    let watchdog: Watchdog | null = null
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

    /** drop closes the current source and its watchdog. */
    const drop = () => {
      watchdog?.clear()
      watchdog = null
      es?.close()
      es = null
    }

    function connect() {
      const src = new EventSource('/api/events')
      es = src
      // A stalled stream is not an auth failure: reopen soon, without
      // counting it as a closed attempt.
      watchdog = armWatchdog(() => {
        if (es !== src || stopped) return
        if (hadOpen) setConnection('lost')
        drop()
        timer = setTimeout(connect, backoffDelay(1))
      })
      src.addEventListener('ping', () => {
        if (es === src) watchdog?.rearm()
      })
      src.addEventListener('state', (e: Event) => {
        if (es !== src) return
        watchdog?.rearm()
        const next = parseState((e as MessageEvent).data)
        if (!next) return
        setDockerUp(next.dockerUp)
        setProjects(next.projects)
        setBackups(next.backups)
        setBatch(next.batch)
        setDrive(next.drive)
        hadOpen = true
        closedInRow = 0
        setConnection('open')
      })
      src.addEventListener('error', () => {
        if (es !== src || stopped) return
        if (hadOpen) setConnection('lost')
        // A CONNECTING source is retried by the browser; a CLOSED one never is.
        if (src.readyState !== CLOSED) return
        drop()
        closedInRow++
        void reconnect()
      })
    }

    connect()
    return () => {
      stopped = true
      clearTimeout(timer)
      drop()
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
      backups,
      batch,
      drive,
      checking,
      recheckDocker,
    }),
    [connection, dockerUp, projects, backups, batch, drive, checking, recheckDocker],
  )
  return <LiveContext.Provider value={value}>{children}</LiveContext.Provider>
}

export function useLive(): Live {
  const ctx = useContext(LiveContext)
  if (!ctx) throw new Error('useLive must be used inside LiveProvider')
  return ctx
}

/** The dim of design 10 while the values on screen may be stale. */
const STALE_STYLE = { opacity: 0.55, filter: 'saturate(0.6)' }

/**
 * LiveDim wraps a page's body (below its header): it dims while the connection
 * is lost or expired. The header, the banners and the dialogs stay outside it.
 */
export function LiveDim({ children, className }: { children: ReactNode; className?: string }) {
  const { connection } = useLive()
  const stale = connection === 'lost' || connection === 'expired'
  return (
    <div data-live-content="" className={className} style={stale ? STALE_STYLE : undefined}>
      {children}
    </div>
  )
}
