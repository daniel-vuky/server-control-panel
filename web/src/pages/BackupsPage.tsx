import { CircleAlert, CloudUpload, TriangleAlert } from 'lucide-react'
import { useId, useState } from 'react'
import { Link } from 'react-router'
import { connectDrive, type DriveIssue, driveIssue } from '../api/drive'
import type { Backup, DriveStatus } from '../api/types.gen'
import { useLive } from '../app/live'
import { BackupSteps } from '../components/BackupProgress'
import { DRIVE_NOT_SET_UP, waitingLine } from '../lib/backup'
import { formatClock } from '../lib/time'
import { usePageRestore } from '../lib/usePageRestore'

const BUTTON =
  'flex h-9 flex-none cursor-pointer items-center rounded-[7px] px-4 font-semibold focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-60'

/** running first, then the waiting ones in queue order. */
const byQueue = (a: Backup, b: Backup) =>
  (a.state === 'running' ? 0 : 1) - (b.state === 'running' ? 0 : 1) ||
  a.queuePosition - b.queuePosition ||
  a.id - b.id

/**
 * BackupsPage is /backups, minimal in this flow (design 04): the Drive
 * folder, the Drive banners, and one card per running or waiting backup.
 */
export default function BackupsPage() {
  const live = useLive()
  const drive = live.drive
  const folder = drive?.state === 'connected' ? drive.folder : null
  const active = [...live.backups].sort(byQueue)

  return (
    <div className="flex max-w-[1040px] flex-col gap-5">
      <header className="flex flex-col gap-1.5">
        <h1 className="text-2xl font-semibold tracking-tight">Backups</h1>
        {folder && (
          <p className="m-0 text-[13px] text-muted">
            Stored in <span className="font-mono text-text-2">Drive/{folder.name}</span>
          </p>
        )}
      </header>

      {drive && <DriveBanner drive={drive} />}

      {live.projects === null ? (
        <p role="status" className="m-0 text-[13px] text-muted">
          Loading backups…
        </p>
      ) : active.length === 0 ? (
        <div className="flex flex-col items-center gap-2.5 rounded-[10px] border border-dashed border-border-strong px-5 py-8 text-center">
          <CloudUpload aria-hidden="true" size={32} strokeWidth={1.4} className="text-[#4a515c]" />
          <p className="m-0 text-[15px] font-semibold">No backup is running.</p>
          <p className="m-0 text-[13px] text-muted">
            The list of all backups arrives with the next backup flow.
          </p>
        </div>
      ) : (
        active.map((b) => <ActiveCard key={b.id} backup={b} />)
      )}
    </div>
  )
}

/** DriveBanner is design 04's banner for Drive not connected or expired; none when connected. */
function DriveBanner({ drive }: { drive: DriveStatus }) {
  const [busy, setBusy] = useState(false)
  const [issue, setIssue] = useState<DriveIssue | null>(null)
  usePageRestore(() => setBusy(false))

  if (drive.state === 'connected') return null
  if (drive.state === 'not_configured') {
    return (
      <div className="flex items-center gap-3 rounded-[10px] border border-border-strong bg-[#12161c] px-4 py-3.5 text-[13px] text-text-2">
        <CircleAlert aria-hidden="true" size={18} strokeWidth={1.8} className="flex-none" />
        {DRIVE_NOT_SET_UP}
      </div>
    )
  }

  const expired = drive.state === 'expired'
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
  const label = expired ? 'Reconnect Google Drive' : 'Connect Google Drive'
  const failed = issue && (
    <span className="basis-full text-[13px] text-danger">{issue.message}</span>
  )

  if (expired) {
    return (
      <div
        role="alert"
        className="flex flex-wrap items-center gap-3 rounded-[10px] border border-[#5c4a1e] bg-[#221c0e] px-4 py-3 text-[#f7e2a6]"
      >
        <TriangleAlert
          aria-hidden="true"
          size={18}
          strokeWidth={2}
          className="flex-none text-warning"
        />
        <span>
          <b>Google Drive token has expired.</b> Backups will fail until you reconnect.
        </span>
        <button
          type="button"
          disabled={busy}
          onClick={() => void connect()}
          className={`${BUTTON} ml-auto h-[34px] border border-[#8a6d1e] bg-transparent px-3.5 text-[#f7e2a6]`}
        >
          {label}
        </button>
        {failed}
      </div>
    )
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
        {label}
      </button>
      {failed}
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
          Triggered by {b.startedBy || 'someone'} · {formatClock(b.createdAt)}
        </span>
        {!running && <span className="text-[12.5px] text-muted">{waitingLine(b)}</span>}
      </div>
      <BackupSteps backup={b} />
    </section>
  )
}
