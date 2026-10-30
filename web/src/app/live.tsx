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

type Live = {
  /** null until the first state event arrives. */
  dockerUp: boolean | null
  /** null until the first state event arrives. */
  projects: Project[] | null
  /** True while a Docker re-check is in flight. */
  checking: boolean
  recheckDocker(): Promise<void>
}

const LiveContext = createContext<Live | null>(null)

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
  const [state, setState] = useState<LiveState | null>(null)
  const [checking, setChecking] = useState(false)

  useEffect(() => {
    if (typeof EventSource === 'undefined') return
    const es = new EventSource('/api/events')
    const onState = (e: Event) => {
      const next = parseState((e as MessageEvent).data)
      if (next) setState(next)
    }
    es.addEventListener('state', onState)
    return () => {
      es.close()
    }
  }, [])

  const recheckDocker = useCallback(async () => {
    setChecking(true)
    try {
      const r = await checkDocker()
      // The server also broadcasts; this makes the banner answer at once.
      setState((s) => (s ? { ...s, dockerUp: r.dockerUp } : { dockerUp: r.dockerUp, projects: [] }))
    } catch {
      // A failed check leaves dockerUp as it was; the next state event corrects it.
    } finally {
      setChecking(false)
    }
  }, [])

  const value = useMemo<Live>(
    () => ({
      dockerUp: state ? state.dockerUp : null,
      projects: state ? state.projects : null,
      checking,
      recheckDocker,
    }),
    [state, checking, recheckDocker],
  )
  return <LiveContext.Provider value={value}>{children}</LiveContext.Provider>
}

export function useLive(): Live {
  const ctx = useContext(LiveContext)
  if (!ctx) throw new Error('useLive must be used inside LiveProvider')
  return ctx
}
