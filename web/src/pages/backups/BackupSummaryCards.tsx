import { type ReactNode, useId } from 'react'
import type { BackupSummary, SummaryDay, TimeZone } from '../../api/types.gen'
import LoadError from '../../components/LoadError'
import { formatBytes } from '../../lib/format'
import { plural } from '../../lib/plural'
import { zoneLabel, zoneParts } from '../../lib/schedule'

type Props = {
  /** The last summary the server sent; null until the first answers. */
  summary: BackupSummary | null
  /** The server zone; null until /api/backup-settings answers (the run's own offset is used meanwhile). */
  zone: TimeZone | null
  /** Why the summary could not be read; shown only while there is none to show. */
  error: string
  onRetry(): void
}

/** NO_ZONE makes zoneParts read an instant in the offset its own text carries. */
const NO_ZONE: TimeZone = { name: '', offset: '' }

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']

/** shortDate is "Oct 26" of a YYYY-MM-DD date. */
function shortDate(date: string): string {
  const [, m, d] = date.split('-').map(Number)
  return `${MONTHS[m - 1] ?? ''} ${d}`
}

/** addDays is the YYYY-MM-DD date n days after date (calendar arithmetic, no zone). */
function addDays(date: string, n: number): string {
  const [y, m, d] = date.split('-').map(Number)
  return new Date(Date.UTC(y, m - 1, d + n)).toISOString().slice(0, 10)
}

/** dayWord is "today", "yesterday" or "Oct 26" for a date, against the server's today. */
function dayWord(date: string, today: string): string {
  if (date === today) return 'today'
  if (date === addDays(today, -1)) return 'yesterday'
  return shortDate(date)
}

/**
 * BackupSummaryCards are design 04's four cards (spec decision 22): what the
 * panel stored on Drive, the succeeded and failed backups of the last 7 days
 * of the server zone, and the next scheduled run, read in the server zone.
 */
export default function BackupSummaryCards({ summary, zone, error, onRetry }: Props) {
  if (summary === null) {
    if (error !== '') {
      return (
        <LoadError title="Could not load the backup summary." message={error} onRetry={onRetry} />
      )
    }
    return (
      <section aria-label="Backup summary" aria-busy="true" className={GRID}>
        {[0, 1, 2, 3].map((i) => (
          <div key={i} className={`${CARD} h-[104px]`}>
            <div className="h-3 w-2/5 rounded bg-border motion-safe:animate-pulse" />
            <div className="h-6 w-3/5 rounded bg-border motion-safe:animate-pulse" />
          </div>
        ))}
      </section>
    )
  }

  const z = zone ?? NO_ZONE
  // The server's today is the last of the 7 days it counted.
  const today = summary.days.at(-1)?.date ?? zoneParts(new Date().toISOString(), z)?.date ?? ''
  const failures = summary.recentFailures
    .map((f) => {
      const date = zoneParts(f.at, z)?.date
      return `${f.projectName} · ${date ? dayWord(date, today) : '—'}`
    })
    .join(', ')

  return (
    <section aria-label="Backup summary" className={GRID}>
      <Card label="Drive usage">
        <span className={VALUE}>{formatBytes(summary.driveBytes)}</span>
        <span className={SUB}>
          {plural(summary.driveFiles, 'file', 'files')} ·{' '}
          {plural(summary.driveProjects, 'project', 'projects')}
        </span>
        <span className={SUB}>stored by the panel</span>
      </Card>
      <Card label="Succeeded · 7 days">
        <span className={`${VALUE} text-success`}>{summary.ok7d}</span>
        <DayStrip days={summary.days} />
      </Card>
      <Card label="Failed · 7 days" alarm={summary.failed7d > 0}>
        <span className={`${VALUE} ${summary.failed7d > 0 ? 'text-danger-text' : ''}`}>
          {summary.failed7d}
        </span>
        <span className={SUB}>{failures === '' ? 'No failures' : failures}</span>
      </Card>
      <NextRun summary={summary} zone={zone} today={today} />
    </section>
  )
}

const GRID = 'grid grid-cols-[repeat(auto-fit,minmax(min(220px,100%),1fr))] gap-3'
const CARD = 'flex flex-col gap-2 rounded-[10px] border bg-surface p-4'
const VALUE = 'font-mono text-2xl font-semibold tabular-nums'
const SUB = 'text-[12.5px] text-muted'

function Card({
  label,
  alarm = false,
  children,
}: {
  label: string
  alarm?: boolean
  children: ReactNode
}) {
  const id = useId()
  return (
    <section
      aria-labelledby={id}
      className={`${CARD} ${alarm ? 'border-error-border' : 'border-border'}`}
    >
      <h2 id={id} className="m-0 text-xs font-normal tracking-[0.06em] text-muted uppercase">
        {label}
      </h2>
      {children}
    </section>
  )
}

const DAY_TONE: Record<string, string> = {
  ok: 'bg-switch-on',
  failed: 'bg-stop',
  none: 'bg-border',
}

/** DayStrip is one bar per day, oldest first: red when anything failed, green when something succeeded. */
function DayStrip({ days }: { days: SummaryDay[] }) {
  return (
    <ul aria-label="Last 7 days" className="m-0 flex h-3.5 list-none items-end gap-[3px] p-0">
      {days.map((d) => {
        const state = d.failed > 0 ? 'failed' : d.ok > 0 ? 'ok' : 'none'
        return (
          <li
            key={d.date}
            data-state={state}
            title={`${shortDate(d.date)}: ${d.ok} succeeded, ${d.failed} failed`}
            className={`h-full flex-1 rounded-[2px] ${DAY_TONE[state]}`}
          >
            <span className="sr-only">
              {shortDate(d.date)}: {d.ok} succeeded, {d.failed} failed
            </span>
          </li>
        )
      })}
    </ul>
  )
}

/**
 * NextRun is the earliest scheduled run in the server zone, the projects due
 * then and the zone named; before the zone is known, the run's own offset.
 */
function NextRun({
  summary,
  zone,
  today,
}: {
  summary: BackupSummary
  zone: TimeZone | null
  today: string
}) {
  const next = summary.nextRun
  const p = next ? zoneParts(next.at, zone ?? NO_ZONE) : null
  if (next === null || p === null) {
    return (
      <Card label="Next run">
        <span className={`${VALUE} text-text-2`}>No schedules</span>
        <span className={SUB}>Set one in a project's Database &amp; Backup tab.</span>
      </Card>
    )
  }
  const day = p.date === today ? 'Today' : p.date === addDays(today, 1) ? 'Tomorrow' : p.day
  const offset = /([+-]\d{2}:\d{2})$/.exec(next.at)?.[1] ?? ''
  const label = zone ? zoneLabel(zone) : offset ? `UTC${offset}` : 'UTC'
  return (
    <Card label="Next run">
      <span className={VALUE}>{p.time}</span>
      <span className={SUB}>
        {day} · {next.projects.join(', ')}
      </span>
      <span className={SUB}>{label}</span>
    </Card>
  )
}
