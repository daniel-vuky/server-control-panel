import { ExternalLink, RotateCw } from 'lucide-react'
import { type ReactNode, useEffect, useId, useRef, useState } from 'react'
import { getBackup } from '../api/backups'
import type { Backup, BackupStep } from '../api/types.gen'
import { useLive } from '../app/live'
import {
  backupLine,
  isActive,
  isRetryable,
  percentOf,
  shownSteps,
  stateWord,
  stepLabel,
  waitingLine,
} from '../lib/backup'
import Dialog, { PRIMARY_BUTTON, SECONDARY_BUTTON } from './Dialog'

const Spinner = ({ testId, size = 11 }: { testId?: string; size?: number }) => (
  <span
    aria-hidden="true"
    data-testid={testId}
    style={{ width: size, height: size }}
    className="box-border flex-none rounded-full border-2 border-accent border-r-transparent motion-safe:animate-[spin_0.8s_linear_infinite]"
  />
)

const Circle = ({ testId }: { testId: string }) => (
  <span
    aria-hidden="true"
    data-testid={testId}
    className="box-border size-[11px] flex-none rounded-full border-2 border-[#3a414b]"
  />
)

const Check = () => (
  <svg
    aria-hidden="true"
    data-testid="step-done"
    width="14"
    height="14"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth="2.4"
    strokeLinecap="round"
    strokeLinejoin="round"
  >
    <path d="m5 12 5 5 9-10" />
  </svg>
)

const Cross = () => (
  <svg
    aria-hidden="true"
    data-testid="step-failed"
    width="14"
    height="14"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    strokeWidth="2.4"
    strokeLinecap="round"
  >
    <path d="M6 6l12 12M18 6 6 18" />
  </svg>
)

/** The look of each step state, from designs 04 and 09. */
const STEP_LOOK: Record<string, { item: string; head: string; icon: () => ReactNode }> = {
  done: {
    item: 'bg-surface border-border',
    head: 'font-medium text-success',
    icon: () => <Check />,
  },
  running: {
    item: 'bg-surface border-[#22344a]',
    head: 'font-semibold text-[#7fb8ff]',
    icon: () => <Spinner testId="step-running" />,
  },
  failed: {
    item: 'bg-[#1e0f11] border-[#6b262c]',
    head: 'font-semibold text-[#ff8c8f]',
    icon: () => <Cross />,
  },
  skipped: {
    item: 'bg-surface border-border',
    head: 'text-muted',
    icon: () => <Circle testId="step-skipped" />,
  },
  waiting: {
    item: 'bg-surface border-border',
    head: 'text-muted',
    icon: () => <Circle testId="step-waiting" />,
  },
}

/** StepItem is one step: its numbered name and icon, then its detail, error or Skipped. */
function StepItem({ step, backup }: { step: BackupStep; backup: Backup }) {
  const look = STEP_LOOK[step.state] ?? STEP_LOOK.waiting
  const pct = step.state === 'running' ? percentOf(step) : null
  let detail: ReactNode = null
  if (step.state === 'failed') {
    detail = (
      <span className="flex flex-col gap-1 text-[12.5px] leading-[1.45] text-error-text">
        {backup.error !== '' && <span className="break-words">{backup.error}</span>}
        {backup.hint !== '' && <span className="break-words">{backup.hint}</span>}
      </span>
    )
  } else if (step.state === 'skipped') {
    detail = (
      <span className="text-[12.5px] text-muted">
        {step.detail === '' ? 'Skipped' : `Skipped · ${step.detail}`}
      </span>
    )
  } else if (step.detail !== '' && pct !== null) {
    // The progress detail ends "… · 62%": the percent keeps its own box, so the words before it never shift
    // (the separator goes with it).
    const words = step.detail.replace(/\s*·?\s*\d{1,3}%\s*$/, '')
    detail = (
      <span className="flex items-baseline justify-between gap-2 font-mono text-xs text-muted">
        <span className="min-w-0 break-words">{words}</span>
        <span data-testid="step-percent" className="inline-block min-w-[4ch] flex-none text-right">
          {pct}%
        </span>
      </span>
    )
  } else if (step.detail !== '') {
    detail = <span className="font-mono text-xs break-words text-muted">{step.detail}</span>
  }
  return (
    <li
      data-state={step.state}
      aria-current={step.state === 'running' ? 'step' : undefined}
      className={`flex min-w-0 flex-col gap-1.5 rounded-lg border px-3 py-2.5 ${look.item}`}
    >
      <span className={`flex items-center gap-2 text-[13px] ${look.head}`}>
        {look.icon()}
        {stepLabel(step.step)}
      </span>
      {pct !== null && (
        <span aria-hidden="true" className="flex h-1 rounded-sm bg-border">
          <span
            data-testid="step-bar"
            className="rounded-sm bg-accent"
            style={{ width: `${pct}%` }}
          />
        </span>
      )}
      {detail}
    </li>
  )
}

/**
 * BackupSteps shows a backup's four steps (designs 04 and 09): done, running
 * (with the server's progress detail), waiting, failed (with the error and
 * hint) or skipped. Compact is the one-line form ("Uploading to Drive · 62%").
 */
export function BackupSteps({ backup, compact = false }: { backup: Backup; compact?: boolean }) {
  if (compact) {
    const active = isActive(backup)
    return (
      <span
        className={`inline-flex items-center gap-2 text-[13px] ${active ? 'text-[#7fb8ff]' : 'text-muted'}`}
      >
        {backup.state === 'running' && <Spinner />}
        {backupLine(backup)}
      </span>
    )
  }
  return (
    <ol className="m-0 grid list-none grid-cols-[repeat(auto-fit,minmax(min(160px,100%),1fr))] gap-2.5 p-0">
      {shownSteps(backup).map((s) => (
        <StepItem key={s.step} step={s} backup={backup} />
      ))}
    </ol>
  )
}

/** The status pill of each backup state. */
const PILL: Record<string, string> = {
  queued: 'bg-[rgba(154,163,175,0.12)] text-stopped',
  running: 'bg-[rgba(76,155,245,0.14)] text-accent',
  ok: 'bg-[rgba(63,207,142,0.12)] text-success',
  failed: 'bg-[rgba(242,85,90,0.14)] text-[#ff8c8f]',
  refused: 'bg-[rgba(242,85,90,0.14)] text-[#ff8c8f]',
  interrupted: 'bg-[rgba(229,185,59,0.13)] text-warning',
}

function Pill({ state }: { state: string }) {
  return (
    <span
      data-testid="backup-pill"
      className={`ml-auto rounded-full px-[9px] py-[3px] text-xs font-medium whitespace-nowrap ${PILL[state] ?? PILL.queued}`}
    >
      {stateWord(state)}
    </span>
  )
}

/** Fetched is one GET answer, with the live list it was asked for against. */
type Fetched = { id: number; backup: Backup | null; error: string; liveAt: Backup[] }

/**
 * useBackupView is backup id as the dialog shows it: from the live state while
 * it is active; otherwise (on opening a backup that is not active, and once
 * each time it leaves the active list) one GET /api/backups/{id}. Until that
 * answers, the last live value stays on screen. When the answer is still
 * active (queued or running) but a later live state lacks it, it is read once
 * more: one GET per such state, never one per event otherwise.
 */
function useBackupView(id: number): { backup: Backup | null; error: string } {
  const live = useLive()
  const active = live.backups.find((b) => b.id === id) ?? null
  const inLive = active !== null
  const last = useRef<Backup | null>(null)
  if (active !== null) last.current = active
  const [fetched, setFetched] = useState<Fetched | null>(null)
  const [again, setAgain] = useState(0)
  const liveNow = useRef(live.backups)
  liveNow.current = live.backups
  const lastAgain = useRef(again)

  useEffect(() => {
    if (inLive) return
    const ctl = new AbortController()
    // A departure (or another id) drops the old answer; a re-read keeps it on screen.
    if (lastAgain.current === again) setFetched(null)
    lastAgain.current = again
    const liveAt = liveNow.current
    getBackup(id, ctl.signal).then(
      (backup) => setFetched({ id, backup, error: '', liveAt }),
      (err: unknown) => {
        if (ctl.signal.aborted) return
        const error = err instanceof Error && err.message ? err.message : 'Something went wrong.'
        setFetched({ id, backup: null, error, liveAt })
      },
    )
    return () => ctl.abort()
  }, [id, inLive, again])

  // The answer said it was still active, and a newer live state no longer has it.
  const stale =
    !inLive &&
    fetched !== null &&
    fetched.id === id &&
    fetched.backup !== null &&
    isActive(fetched.backup) &&
    fetched.liveAt !== live.backups
  useEffect(() => {
    if (stale) setAgain((n) => n + 1)
  }, [stale])

  if (active !== null) return { backup: active, error: '' }
  if (fetched !== null && fetched.id === id) {
    return {
      backup: fetched.backup ?? (last.current?.id === id ? last.current : null),
      error: fetched.error,
    }
  }
  return { backup: last.current?.id === id ? last.current : null, error: '' }
}

type Props = {
  id: number
  /** retrying is true while a Retry of this backup is being started. */
  retrying: boolean
  onClose(): void
  onRetry(backup: Backup): void
}

/**
 * BackupProgress is design 09's backup progress dialog: the title, a status
 * pill, the four steps and Close (which never cancels), Retry for a failed,
 * refused or interrupted backup, and Open in Drive once it succeeded.
 */
export default function BackupProgress({ id, retrying, onClose, onRetry }: Props) {
  const titleId = useId()
  const close = useRef<HTMLButtonElement>(null)
  const { backup: b, error } = useBackupView(id)
  const failedStep = b ? shownSteps(b).some((s) => s.state === 'failed') : false

  return (
    <Dialog labelledBy={titleId} onClose={onClose} initialFocus={close} width="w-[760px]">
      <div className="flex items-center gap-2.5">
        <h2 id={titleId} className="m-0 min-w-0 text-[17px] font-semibold break-words">
          Backup
          {b && (
            <>
              {' '}
              <span className="font-mono">
                {b.projectName} · {b.database.name}
              </span>
            </>
          )}
        </h2>
        {b && <Pill state={b.state} />}
      </div>

      {error !== '' && <p className="m-0 text-[13px] text-error-text">{error}</p>}
      {b === null ? (
        error === '' && (
          <p role="status" className="m-0 text-[13px] text-muted">
            Loading backup…
          </p>
        )
      ) : (
        <>
          {b.state === 'queued' && <p className="m-0 text-[13px] text-muted">{waitingLine(b)}</p>}
          {!failedStep && b.error !== '' && (
            <p className="m-0 flex flex-col gap-1 rounded-lg border border-[#6b262c] bg-[#1e0f11] px-3 py-2.5 text-[13px] leading-[1.45] text-error-text">
              <span>{b.error}</span>
              {b.hint !== '' && <span>{b.hint}</span>}
            </p>
          )}
          <BackupSteps backup={b} />
        </>
      )}

      <div className="flex flex-wrap justify-end gap-2.5 pt-1">
        <button ref={close} type="button" onClick={onClose} className={SECONDARY_BUTTON}>
          Close
        </button>
        {b?.state === 'ok' && b.driveLink !== '' && (
          <a
            href={b.driveLink}
            target="_blank"
            rel="noopener noreferrer"
            className={PRIMARY_BUTTON}
          >
            <ExternalLink aria-hidden="true" size={15} strokeWidth={2} />
            Open in Drive
          </a>
        )}
        {b && isRetryable(b) && (
          <button
            type="button"
            disabled={retrying}
            aria-busy={retrying || undefined}
            onClick={() => onRetry(b)}
            className={PRIMARY_BUTTON}
          >
            <RotateCw aria-hidden="true" size={15} strokeWidth={2} />
            Retry
          </button>
        )}
      </div>
    </Dialog>
  )
}
