import { Link } from 'react-router'
import type { LatestBackup } from '../../api/types.gen'
import { formatRecent } from '../../lib/time'
import type { StatusFilter } from './filters'

const DOTS = [
  ['running', 'bg-success'],
  ['partial', 'bg-warning'],
  ['stopped', 'bg-stopped'],
  ['error', 'bg-danger'],
] as const

const BADGE_OK = 'bg-[rgba(63,207,142,0.12)] text-success'
const BADGE_BAD = 'bg-[rgba(242,85,90,0.16)] text-danger-text'

const BADGES: Record<string, { word: string; look: string }> = {
  ok: { word: 'OK', look: BADGE_OK },
  failed: { word: 'Failed', look: BADGE_BAD },
  refused: { word: 'Refused', look: BADGE_BAD },
  interrupted: { word: 'Interrupted', look: BADGE_BAD },
}

/**
 * QuickCounts is the line under the server summary (design 01): the settled
 * counts with their dots, the latest ended backup with its badge and a link to
 * the Backups page. `latest` is undefined while the read has no answer (no
 * backup part), null when no project has a backup yet. `now` is for tests.
 */
export default function QuickCounts({
  counts,
  latest,
  now,
}: {
  counts: Record<StatusFilter, number>
  latest: LatestBackup | null | undefined
  now?: Date
}) {
  const badge = latest ? (BADGES[latest.state] ?? BADGES.failed) : null
  return (
    <section
      aria-label="Quick counts"
      className="flex flex-wrap items-center gap-x-[18px] gap-y-2 rounded-[10px] border border-border bg-surface px-4 py-3"
    >
      {DOTS.map(([s, dot]) => (
        <span key={s} className="flex items-center gap-2">
          <span
            data-testid="count-dot"
            aria-hidden="true"
            className={`size-2 rounded-full ${dot}`}
          />
          <b className="font-mono font-semibold">{counts[s]}</b> {s}
        </span>
      ))}
      {latest !== undefined && (
        <>
          <span aria-hidden="true" className="h-[18px] w-px bg-border-strong" />
          <span className="flex items-center gap-2 text-text-2">
            <svg
              aria-hidden="true"
              width="16"
              height="16"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.8"
              strokeLinecap="round"
              strokeLinejoin="round"
              className="text-muted"
            >
              <path d="M7 18a5 5 0 0 1-.9-9.9A6 6 0 0 1 17.7 7 4.5 4.5 0 0 1 18 18z" />
              <path d="M12 11v6M9.5 13.5 12 11l2.5 2.5" />
            </svg>
            {latest === null || badge === null ? (
              'No backups yet'
            ) : (
              <>
                Latest backup:{' '}
                <span className="font-mono">
                  {latest.projectName} · {formatRecent(latest.at, now)}
                </span>
                <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${badge.look}`}>
                  {badge.word}
                </span>
              </>
            )}
          </span>
        </>
      )}
      <Link
        to="/backups"
        className="ml-auto text-[13px] text-accent-text hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      >
        View Backups →
      </Link>
    </section>
  )
}
