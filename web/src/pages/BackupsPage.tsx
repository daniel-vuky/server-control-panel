import { CircleAlert } from 'lucide-react'
import { useCallback, useEffect, useId, useRef, useState } from 'react'
import { Link, useSearchParams } from 'react-router'
import { getBackupSettings, getBackupSummary } from '../api/backups'
import { connectDrive, type DriveIssue, driveIssue } from '../api/drive'
import type { Backup, BackupSummary, DriveStatus } from '../api/types.gen'
import { useLive } from '../app/live'
import { BackupSteps } from '../components/BackupProgress'
import CloudUploadIcon from '../components/CloudUploadIcon'
import { DRIVE_NOT_SET_UP, waitingLine } from '../lib/backup'
import { formatClock } from '../lib/time'
import { useBackup } from '../lib/useBackup'
import { useLoad } from '../lib/useLoad'
import { usePageRestore } from '../lib/usePageRestore'
import { BackUpAllButton, type BackupAll, BackupAllBar, useBackupAll } from './backups/BackupAllBar'
import BackupSummaryCards from './backups/BackupSummaryCards'
import BackupsTable, {
  BackupFilters,
  filtersOf,
  isFiltered,
  useAllBackups,
} from './backups/BackupsTable'

const BUTTON =
  'flex h-9 flex-none cursor-pointer items-center rounded-[7px] px-4 font-semibold focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-60'

/** running first, then the waiting ones in queue order. */
const byQueue = (a: Backup, b: Backup) =>
  (a.state === 'running' ? 0 : 1) - (b.state === 'running' ? 0 : 1) ||
  a.queuePosition - b.queuePosition ||
  a.id - b.id

/**
 * useSummary is GET /api/backups/summary, read once `ready` (the first live
 * state arrived) and again whenever `changed` changes; a failed re-read keeps
 * the last summary.
 */
function useSummary(changed: string, ready: boolean) {
  const [summary, setSummary] = useState<BackupSummary | null>(null)
  const [error, setError] = useState('')
  const ctl = useRef<AbortController | null>(null)

  const read = useCallback(() => {
    ctl.current?.abort()
    const c = new AbortController()
    ctl.current = c
    setError('')
    getBackupSummary(c.signal).then(
      (s) => {
        if (!c.signal.aborted) setSummary(s)
      },
      (err: unknown) => {
        if (c.signal.aborted) return
        setError(err instanceof Error && err.message ? err.message : 'Something went wrong.')
      },
    )
  }, [])

  // biome-ignore lint/correctness/useExhaustiveDependencies: changed re-reads the summary.
  useEffect(() => {
    if (ready) read()
  }, [changed, ready, read])
  useEffect(() => () => ctl.current?.abort(), [])

  return { summary, error, retry: read }
}

/**
 * BackupsPage is /backups (design 04, spec decisions 17 to 23): Back up all
 * now with its progress and result, the Drive banner, the summary cards, the
 * running and waiting backups, then every backup, filtered by the URL query,
 * 50 at a time; or the empty state when there is no backup at all.
 */
export default function BackupsPage() {
  const live = useLive()
  const drive = live.drive
  const folder = drive?.state === 'connected' ? drive.folder : null
  const active = [...live.backups].sort(byQueue)
  const flow = useBackup()
  const all = useBackupAll()
  const zone = useLoad(getBackupSettings).load
  const [params, setParams] = useSearchParams()
  const filters = filtersOf(params)

  // What the summary and the table re-read on: a backup queued, started or
  // ended, the batch moved, or a refusal stored a row (never mere progress).
  const b = live.batch
  const changed = [
    live.backups.map((x) => `${x.id}:${x.state}`).join(','),
    b ? `${b.id}:${b.done}:${b.finishedAt ?? ''}` : '',
    flow.refusals,
  ].join('|')
  // The first live state is the baseline of `changed`: read once it arrived, not twice.
  const ready = live.projects !== null
  const summary = useSummary(changed, ready)
  const table = useAllBackups(filters, changed, ready)
  const timeZone = zone.state === 'ok' ? zone.data.timeZone : null

  const setFilter = (key: 'project' | 'result' | 'from' | 'to', value: string) => {
    const next = new URLSearchParams(params)
    if (value === '') next.delete(key)
    else next.set(key, value)
    setParams(next, { replace: true })
  }

  const empty =
    table.loaded && table.error === '' && table.rows.length === 0 && !isFiltered(filters)

  return (
    <div className="flex max-w-[1240px] flex-col gap-5">
      <header className="flex flex-wrap items-end justify-between gap-4">
        <div className="flex flex-col gap-1.5">
          <h1 className="m-0 text-2xl font-semibold tracking-tight">Backups</h1>
          <p className="m-0 text-[13px] text-muted">
            All project database backups
            {folder && (
              <>
                {' · stored in '}
                <span className="font-mono text-text-2">Drive/{folder.name}</span>
              </>
            )}
          </p>
        </div>
        <BackUpAllButton all={all} />
      </header>

      {drive && <DriveBanner drive={drive} />}
      <BackupAllBar all={all} />

      <BackupSummaryCards
        summary={summary.summary}
        zone={timeZone}
        error={summary.error}
        onRetry={summary.retry}
      />

      {active.map((x) => (
        <ActiveCard key={x.id} backup={x} />
      ))}

      {empty ? (
        <NoBackups all={all} />
      ) : (
        <>
          <BackupFilters
            filters={filters}
            projects={live.projects ?? []}
            zone={timeZone}
            set={setFilter}
          />
          <BackupsTable all={table} flow={flow} projects={live.projects ?? []} />
        </>
      )}
      {flow.dialogs}
    </div>
  )
}

/** NoBackups is design 10's empty Backups page, with Back up all now. */
function NoBackups({ all }: { all: BackupAll }) {
  return (
    <div className="flex min-h-[220px] flex-col items-center justify-center gap-2.5 rounded-xl border border-dashed border-border-strong p-5 text-center">
      <CloudUploadIcon size={44} strokeWidth={1.4} stroke="#4A515C" />
      <p className="m-0 font-semibold">No backups yet</p>
      <p className="m-0 text-[13px] leading-normal text-muted">
        Run the first one now or set up a daily schedule.
      </p>
      <BackUpAllButton all={all} />
    </div>
  )
}

/**
 * DriveBanner is design 04's banner for Drive not connected or not set up,
 * and for Drive connected with no folder chosen (Back up all is disabled
 * then). None when ready, and none when expired: the app shell shows that
 * one on every page.
 */
function DriveBanner({ drive }: { drive: DriveStatus }) {
  const [busy, setBusy] = useState(false)
  const [issue, setIssue] = useState<DriveIssue | null>(null)
  usePageRestore(() => setBusy(false))

  if (drive.state === 'expired') return null
  if (drive.state === 'connected') {
    if (drive.folder !== null) return null
    return (
      <div
        role="alert"
        className="flex flex-wrap items-center gap-3 rounded-[10px] border border-border-strong bg-[#12161c] px-4 py-3.5"
      >
        <DriveGlyph />
        <div className="flex flex-col gap-0.5">
          <span className="font-semibold">No Drive folder chosen</span>
          <span className="text-[13px] text-muted">
            Backups need a folder to upload into. Choose one in Settings.
          </span>
        </div>
        <Link to="/settings" className={`${BUTTON} ml-auto bg-text text-bg no-underline`}>
          Choose a folder in Settings
        </Link>
      </div>
    )
  }
  if (drive.state === 'not_configured') {
    return (
      <div className="flex items-center gap-3 rounded-[10px] border border-border-strong bg-[#12161c] px-4 py-3.5 text-[13px] text-text-2">
        <CircleAlert aria-hidden="true" size={18} strokeWidth={1.8} className="flex-none" />
        {DRIVE_NOT_SET_UP}
      </div>
    )
  }

  const connect = async () => {
    if (busy) return
    setBusy(true)
    setIssue(null)
    try {
      const { url } = await connectDrive('/backups')
      window.location.assign(url)
    } catch (err) {
      setIssue(driveIssue(err))
      setBusy(false)
    }
  }

  return (
    <div
      role="alert"
      className="flex flex-wrap items-center gap-3 rounded-[10px] border border-border-strong bg-[#12161c] px-4 py-3.5"
    >
      <DriveGlyph />
      <div className="flex flex-col gap-0.5">
        <span className="font-semibold">Google Drive not connected</span>
        <span className="text-[13px] text-muted">
          Backups need a destination. Connect once and all projects share the same target folder.
        </span>
      </div>
      <button
        type="button"
        disabled={busy}
        onClick={() => void connect()}
        className={`${BUTTON} ml-auto border-0 bg-text text-bg`}
      >
        Connect Google Drive
      </button>
      {issue && <span className="basis-full text-[13px] text-danger">{issue.message}</span>}
    </div>
  )
}

/** DriveGlyph is design 04's Drive mark. */
function DriveGlyph() {
  return (
    <svg
      aria-hidden="true"
      width="20"
      height="20"
      viewBox="0 0 24 24"
      fill="none"
      stroke="#C9CED6"
      strokeWidth="1.8"
      strokeLinejoin="round"
      className="flex-none"
    >
      <path d="M8.5 3h7l6 10.5-3.5 6h-12L2.5 13.5z" />
      <path d="M8.5 3l6.5 11.5M15.5 3 9 14.5M2.5 13.5h19" />
    </svg>
  )
}

/**
 * ActiveCard is design 04's backup in progress: what it backs up (the
 * project links to its Database & Backup tab), who started it and when, and
 * the four steps.
 */
function ActiveCard({ backup: b }: { backup: Backup }) {
  const titleId = useId()
  const running = b.state === 'running'
  return (
    <section
      aria-labelledby={titleId}
      className="flex flex-col gap-3.5 rounded-[10px] border border-[#22344a] bg-[#0f141b] p-4"
    >
      <div className="flex flex-wrap items-center gap-2.5">
        {running ? (
          <span
            aria-hidden="true"
            className="box-border size-3 flex-none rounded-full border-2 border-accent border-r-transparent motion-safe:animate-[spin_0.8s_linear_infinite]"
          />
        ) : (
          <span
            aria-hidden="true"
            className="box-border size-3 flex-none rounded-full border-2 border-[#3a414b]"
          />
        )}
        <h2 id={titleId} className="m-0 text-[14px] font-semibold">
          {running ? 'Backing up' : 'Waiting to back up'}{' '}
          <span className="font-mono">
            <Link
              to={`/projects/${b.projectId}/database`}
              className="text-white no-underline hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
            >
              {b.projectName}
            </Link>{' '}
            · {b.database.name}
          </span>
        </h2>
        <span className="text-[12.5px] text-muted">
          Triggered by {b.startedBy === 'schedule' ? 'Schedule' : b.startedBy || 'someone'} ·{' '}
          {formatClock(b.createdAt)}
        </span>
        {!running && <span className="text-[12.5px] text-muted">{waitingLine(b)}</span>}
      </div>
      <BackupSteps backup={b} />
    </section>
  )
}
