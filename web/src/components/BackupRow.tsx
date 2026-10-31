import { Link } from 'react-router'
import { downloadUrl } from '../api/backups'
import type { Backup, BackupList } from '../api/types.gen'
import { backupLine, isActive, isRetryable } from '../lib/backup'
import { formatBytes, formatDuration } from '../lib/format'
import { formatWhen } from '../lib/time'
import type { BackupFlow } from '../lib/useBackup'

/*
 * BackupRow and its table pieces are shared by the project's Backup history
 * (design 02) and the Backups page's table of every backup (design 04).
 */

// 6 px a side with 16 px at the row ends: the design's 12 px column gaps.
export const HEAD = 'px-1.5 py-2.5 font-normal first:pl-4 last:pr-4'
const CELL = 'px-1.5 py-2.5 align-middle first:pl-4 last:pr-4'
export const ROW_BUTTON =
  'h-7 cursor-pointer rounded-md border border-border-strong bg-transparent px-2.5 font-sans text-[12.5px] text-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-60'
const LINK =
  'font-sans text-[13px] whitespace-nowrap text-text-2 underline hover:text-white focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent'

/** The colour of a status, from designs 02 and 04: text, then its dot. */
const TONE: Record<string, [string, string]> = {
  ok: ['text-success', 'bg-success'],
  failed: ['text-danger-text', 'bg-danger-text'],
  refused: ['text-danger-text', 'bg-danger-text'],
  interrupted: ['text-warning', 'bg-warning'],
  running: ['text-accent-text', 'bg-accent-text'],
  queued: ['text-accent-text', 'bg-accent-text'],
}

/** byNewest sorts backups newest first (ids grow with time). */
export const byNewest = (a: Backup, b: Backup) => b.id - a.id

/**
 * mergeNewest puts a fresh newest page in front of what was shown: rows
 * older than the page are kept (with their `more`), so a re-read never drops
 * the older pages the Operator opened.
 */
export function mergeNewest(
  rows: Backup[],
  more: boolean,
  page: Pick<BackupList, 'backups' | 'more'>,
): { rows: Backup[]; more: boolean } {
  const last = page.backups.at(-1)
  if (last === undefined) return { rows: [], more: false }
  const tail = rows.filter((b) => b.id < last.id)
  return tail.length > 0
    ? { rows: [...page.backups, ...tail], more }
    : { rows: page.backups, more: page.more }
}

export type BackupRowProps = {
  backup: Backup
  several: boolean
  /** Show the Project column (the Backups page lists every project's backups). */
  withProject?: boolean
  flow: BackupFlow
  /** The database's number of copies to keep now, for a removed copy's words; null when unknown. */
  keep: number | null
}

/** triggeredBy is who started a backup: the person's email, or Schedule. */
const triggeredBy = (b: Backup) => (b.startedBy === 'schedule' ? 'Schedule' : b.startedBy || '—')

/** BackupRow is one backup of a history table; the Backups page shares it. */
export function BackupRow({ backup: b, several, withProject = false, flow, keep }: BackupRowProps) {
  const [text, dot] = TONE[b.state] ?? TONE.queued
  const retrying = flow.retryingId === b.id
  const removed = b.prunedAt !== null && b.prunedAt !== undefined && b.prunedAt !== ''
  return (
    <tr data-id={b.id} className="border-b border-divider last:border-b-0">
      <td className={CELL}>{formatWhen(b.createdAt)}</td>
      {withProject && (
        <td className={`${CELL} break-all`}>
          <Link
            to={`/projects/${b.projectId}/database`}
            className="text-white no-underline hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          >
            {b.projectName}
          </Link>
        </td>
      )}
      {several && <td className={`${CELL} break-all text-text-2`}>{b.database.name}</td>}
      <td className={`${CELL} whitespace-nowrap`}>
        {b.state === 'ok' && b.sizeBytes > 0 ? formatBytes(b.sizeBytes) : '—'}
      </td>
      <td className={`${CELL} whitespace-nowrap`}>
        {isActive(b) ? '—' : formatDuration(b.durationMs)}
      </td>
      <td className={CELL}>
        <button
          type="button"
          onClick={() => flow.open(b.id)}
          className={`flex cursor-pointer items-baseline gap-[7px] border-0 bg-transparent p-0 text-left font-sans text-[13px] hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent ${text}`}
        >
          <span
            aria-hidden="true"
            className={`size-[7px] flex-none translate-y-[-1px] rounded-full ${dot}`}
          />
          <span className="break-words">{backupLine(b)}</span>
        </button>
      </td>
      <td className={`${CELL} font-sans break-all text-text-2`}>{triggeredBy(b)}</td>
      {/* History (auto layout): w-px, so the actions take their whole width and Time and Triggered by wrap
          instead. Backups page (table-fixed, a colgroup width): the actions wrap as units, and so does the
          pruned row's "Removed (kept N newest)", rather than spill into the next column. */}
      <td className={`${CELL} w-px ${withProject ? '' : 'whitespace-nowrap'}`}>
        <span
          className={`flex items-center justify-end gap-3 ${withProject ? 'flex-wrap gap-y-1' : 'whitespace-nowrap'}`}
        >
          {removed && (
            <span className="font-sans text-[13px] text-muted">
              {keep === null ? 'Removed' : `Removed (kept ${keep} newest)`}
            </span>
          )}
          {b.state === 'ok' && !removed && (
            <>
              {b.driveLink !== '' && (
                <a href={b.driveLink} target="_blank" rel="noopener noreferrer" className={LINK}>
                  Open in Drive
                </a>
              )}
              {/* No download attribute: the server sends Content-Disposition, and a
                  failure is a page to read, not a file to save. */}
              <a href={downloadUrl(b.id)} className={LINK}>
                Download
              </a>
            </>
          )}
          {isRetryable(b) && (
            <button
              type="button"
              disabled={retrying}
              aria-busy={retrying || undefined}
              onClick={() => void flow.retry(b)}
              className={ROW_BUTTON}
            >
              Retry
            </button>
          )}
        </span>
      </td>
    </tr>
  )
}
