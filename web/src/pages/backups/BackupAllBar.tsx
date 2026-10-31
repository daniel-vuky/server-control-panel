import { CircleCheck, CircleX, X } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { backUpAll, backupRefusal } from '../../api/backups'
import type { BackupAllResult, BackupBatch } from '../../api/types.gen'
import { useLive } from '../../app/live'
import CloudUploadIcon from '../../components/CloudUploadIcon'
import { useToast } from '../../components/Toast'

/** DISMISSED is the localStorage key of the last Back up all result the Operator dismissed. */
const DISMISSED = 'scp.backupAll.dismissed'
/** STARTED is the sessionStorage key of the Back up all this tab started (its toast). */
const STARTED = 'scp.backupAll.started'

/** readId reads a batch id from storage; null when absent, not a number or storage is denied. */
function readId(storage: () => Storage, key: string): number | null {
  try {
    const v = Number(storage().getItem(key))
    return Number.isInteger(v) && v > 0 ? v : null
  } catch {
    return null
  }
}

/** writeId stores a batch id (null removes it); a denied storage is ignored. */
function writeId(storage: () => Storage, key: string, id: number | null) {
  try {
    if (id === null) storage().removeItem(key)
    else storage().setItem(key, String(id))
  } catch {
    // The page works without storage: the state in memory still holds it.
  }
}

const local = () => window.localStorage
const session = () => window.sessionStorage

/** resultWords is "Back up all finished: 5 succeeded, 1 failed." (spec decision 19). */
export const resultWords = (b: BackupBatch): string =>
  `Back up all finished: ${b.ok} succeeded, ${b.failed} failed.`

/** skippedWords is the line for databases Back up all left out, or '' when none were. */
export const skippedWords = (n: number): string =>
  n <= 0
    ? ''
    : n === 1
      ? '1 database was skipped: not supported.'
      : `${n} databases were skipped: not supported.`

/**
 * shownBatch is the batch the page shows: the live one, or the one this tab
 * just started while the live state has not caught up with it yet.
 */
function shownBatch(live: BackupBatch | null, started: BackupAllResult | null): BackupBatch | null {
  if (started === null || (live !== null && live.id >= started.id)) return live
  return {
    id: started.id,
    startedBy: '',
    startedAt: '',
    total: started.total,
    done: 0,
    ok: 0,
    failed: 0,
    skipped: started.skipped,
    finishedAt: null,
  }
}

/**
 * useBackupAll is Back up all now (spec decisions 17 to 19): enabled only
 * while Drive is connected with a folder and no batch runs, the POST, the batch as the
 * live state reports it, the result's dismissal per batch id (kept in
 * localStorage when it can be) and the toast in the tab that started it.
 */
export function useBackupAll() {
  const live = useLive()
  const toast = useToast()
  const [posting, setPosting] = useState(false)
  const [started, setStarted] = useState<BackupAllResult | null>(null)
  const [dismissed, setDismissed] = useState<number | null>(() => readId(local, DISMISSED))
  // The batch this tab started and has not toasted yet (also across a reload of the tab).
  const mine = useRef<number | null>(readId(session, STARTED))
  const mounted = useRef(true)

  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])

  const batch = shownBatch(live.batch, started)
  const running = batch !== null && batch.finishedAt === null
  const connected = live.drive?.state === 'connected'
  // The server refuses a batch with no folder to upload into (folder_missing).
  const folder = connected && live.drive?.folder != null

  const finished = live.batch?.finishedAt ? live.batch : null
  // biome-ignore lint/correctness/useExhaustiveDependencies: started re-runs it for a batch that ended before its POST answered.
  useEffect(() => {
    if (finished === null || mine.current !== finished.id) return
    mine.current = null
    writeId(session, STARTED, null)
    const skipped = skippedWords(finished.skipped)
    if (finished.failed > 0) toast.error(resultWords(finished), skipped || undefined)
    else toast.show(skipped ? `${resultWords(finished)} ${skipped}` : resultWords(finished))
  }, [finished, started, toast])

  const start = useCallback(async () => {
    if (posting) return
    setPosting(true)
    try {
      const res = await backUpAll()
      mine.current = res.id
      writeId(session, STARTED, res.id)
      if (mounted.current) setStarted(res)
    } catch (err) {
      const words = err instanceof Error && err.message ? err.message : 'Something went wrong.'
      toast.error("Couldn't start Back up all", backupRefusal(err)?.message || words)
    } finally {
      if (mounted.current) setPosting(false)
    }
  }, [posting, toast])

  const dismiss = useCallback((id: number) => {
    setDismissed(id)
    writeId(local, DISMISSED, id)
  }, [])

  const reason = !connected
    ? 'Connect Google Drive first'
    : !folder
      ? 'Choose a Drive folder in Settings first'
      : running
        ? 'Back up all is running'
        : ''

  return {
    start,
    enabled: folder && !running && !posting,
    posting,
    reason,
    /** The batch to show: running, or finished and not dismissed. */
    batch: batch !== null && (batch.finishedAt === null || batch.id !== dismissed) ? batch : null,
    dismiss,
  }
}

export type BackupAll = ReturnType<typeof useBackupAll>

/** BackUpAllButton is design 04's header button (and the empty state's). */
export function BackUpAllButton({ all }: { all: BackupAll }) {
  return (
    <button
      type="button"
      disabled={!all.enabled}
      aria-busy={all.posting || undefined}
      title={all.reason || undefined}
      onClick={() => void all.start()}
      className="flex h-[38px] flex-none cursor-pointer items-center gap-2 rounded-lg border-0 bg-text px-4 font-semibold text-bg focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:bg-selected disabled:text-muted"
    >
      <CloudUploadIcon size={15} strokeWidth={2} />
      Back up all now
    </button>
  )
}

/**
 * BackupAllBar is the batch while it runs ("Back up all: 2 of 6 done ·
 * backing up crm · crm_db") and its result once it ends, until dismissed.
 */
export function BackupAllBar({ all }: { all: BackupAll }) {
  const live = useLive()
  const b = all.batch
  if (b === null) return null

  if (b.finishedAt === null) {
    const now = live.backups.find((x) => x.state === 'running')
    return (
      <div
        role="status"
        data-testid="backup-all"
        className="flex flex-col gap-2 rounded-[10px] border border-info-border bg-info-surface px-4 py-3"
      >
        <span className="flex items-center gap-2.5 text-[13px]">
          <span
            aria-hidden="true"
            className="box-border size-3 flex-none rounded-full border-2 border-accent border-r-transparent motion-safe:animate-[spin_0.8s_linear_infinite]"
          />
          <span>
            Back up all: {b.done} of {b.total} done
            {now && (
              <>
                {' · backing up '}
                <span className="font-mono">
                  {now.projectName} · {now.database.name}
                </span>
              </>
            )}
          </span>
        </span>
        <span aria-hidden="true" className="flex h-1 overflow-hidden rounded-sm bg-border">
          <span
            className="rounded-sm bg-accent"
            style={{ width: `${b.total > 0 ? Math.round((b.done / b.total) * 100) : 0}%` }}
          />
        </span>
      </div>
    )
  }

  const failed = b.failed > 0
  const skipped = skippedWords(b.skipped)
  const Icon = failed ? CircleX : CircleCheck
  return (
    <div
      role="status"
      data-testid="backup-all"
      className={`flex items-start gap-3 rounded-[10px] border px-4 py-3 text-[13px] ${failed ? 'border-error-border bg-error-soft' : 'border-success-bg bg-[#0f1813]'}`}
    >
      <Icon
        aria-hidden="true"
        size={18}
        strokeWidth={1.8}
        className={`mt-px flex-none ${failed ? 'text-danger' : 'text-success'}`}
      />
      <div className="flex min-w-0 flex-col gap-0.5">
        <span className="font-semibold">{resultWords(b)}</span>
        {skipped && <span className="text-muted">{skipped}</span>}
      </div>
      <button
        type="button"
        aria-label="Dismiss"
        onClick={() => all.dismiss(b.id)}
        className="-my-0.5 ml-auto flex size-6 flex-none cursor-pointer items-center justify-center rounded-md border-0 bg-transparent text-muted hover:text-text focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-accent"
      >
        <X aria-hidden="true" size={14} strokeWidth={2} />
      </button>
    </div>
  )
}
