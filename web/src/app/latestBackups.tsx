import { createContext, type ReactNode, useContext, useEffect, useMemo, useState } from 'react'
import { getLatestBackups } from '../api/backups'
import type { LatestBackups } from '../api/types.gen'
import { backupChanges } from '../lib/backupChanges'
import { useLive } from './live'

type Latest = { data: LatestBackups | null; error: boolean }

const NONE: Latest = { data: null, error: false }

const LatestBackupsContext = createContext<Latest>(NONE)

/**
 * LatestBackupsProvider is the one reader of /api/backups/latest: it reads once
 * the first live state arrives and again whenever a backup is queued, started
 * or ended or the batch moves (the Backups page's `changed` key), so a backup
 * event costs one request whatever page is open. It sits inside LiveProvider.
 * A failed read keeps the last answer.
 */
export function LatestBackupsProvider({ children }: { children: ReactNode }) {
  const live = useLive()
  const ready = live.projects !== null
  const changed = backupChanges(live)
  const [data, setData] = useState<LatestBackups | null>(null)
  const [failed, setFailed] = useState(false)

  // biome-ignore lint/correctness/useExhaustiveDependencies: changed re-reads.
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
  }, [changed, ready])

  const value = useMemo<Latest>(() => ({ data, error: data === null && failed }), [data, failed])
  return <LatestBackupsContext.Provider value={value}>{children}</LatestBackupsContext.Provider>
}

/**
 * useLatestBackups is the shell's answer: each tracked project's newest ended
 * backup, or no data (and no error) outside the shell.
 */
export function useLatestBackups(): Latest {
  return useContext(LatestBackupsContext)
}
