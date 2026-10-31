import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from 'react'
import { getLatestBackups } from '../api/backups'
import type { LatestBackups } from '../api/types.gen'
import { backupChanges } from '../lib/backupChanges'
import { useVisibleInterval } from '../lib/useVisibleInterval'
import { useLive } from './live'

type Latest = {
  data: LatestBackups | null
  error: boolean
  /** refresh reads the answer again now; a no-op outside the shell. */
  refresh: () => void
}

const NONE: Latest = { data: null, error: false, refresh: () => {} }

/** SLOW_READ_MS is the re-read period that catches what no live event signals (a scheduled refusal). */
const SLOW_READ_MS = 60_000

const LatestBackupsContext = createContext<Latest>(NONE)

/**
 * LatestBackupsProvider is the one reader of /api/backups/latest: it reads once
 * the first live state arrives and again whenever a backup is queued, started
 * or ended or the batch moves (the Backups page's `changed` key), so a backup
 * event costs one request whatever page is open. A refused backup writes a row
 * but never enters the live backups, so a refusal the browser caused calls
 * `refresh()`, and a slow re-read every 60 s while the tab is visible catches a
 * scheduled one (no client signal). It sits inside LiveProvider. A failed read
 * keeps the last answer.
 */
export function LatestBackupsProvider({ children }: { children: ReactNode }) {
  const live = useLive()
  const ready = live.projects !== null
  const changed = backupChanges(live)
  const [data, setData] = useState<LatestBackups | null>(null)
  const [failed, setFailed] = useState(false)
  const [bump, setBump] = useState(0)
  const refresh = useCallback(() => setBump((n) => n + 1), [])
  useVisibleInterval(refresh, SLOW_READ_MS)

  // biome-ignore lint/correctness/useExhaustiveDependencies: changed and bump re-read.
  useEffect(() => {
    if (!ready) return
    const ctl = new AbortController()
    getLatestBackups(ctl.signal).then(
      (d) => {
        if (ctl.signal.aborted) return
        setData(d)
        setFailed(false)
      },
      () => {
        if (!ctl.signal.aborted) setFailed(true)
      },
    )
    return () => ctl.abort()
  }, [changed, ready, bump])

  const value = useMemo<Latest>(
    () => ({ data, error: data === null && failed, refresh }),
    [data, failed, refresh],
  )
  return <LatestBackupsContext.Provider value={value}>{children}</LatestBackupsContext.Provider>
}

/**
 * useLatestBackups is the shell's answer: each tracked project's newest ended
 * backup, or no data (and no error, a no-op refresh) outside the shell.
 */
export function useLatestBackups(): Latest {
  return useContext(LatestBackupsContext)
}
