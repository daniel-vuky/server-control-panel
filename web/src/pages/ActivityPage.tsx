import { Activity, SearchX } from 'lucide-react'
import { type ReactNode, useCallback, useEffect, useId, useRef, useState } from 'react'
import { useSearchParams } from 'react-router'
import { getActivity } from '../api/activity'
import { getBackupSettings } from '../api/backups'
import type { ActivityEntry, Project } from '../api/types.gen'
import { LiveDim, useLive } from '../app/live'
import EmptyState, { EmptyStateButton } from '../components/EmptyState'
import LoadError from '../components/LoadError'
import PageHeader from '../components/PageHeader'
import { SkeletonCard } from '../components/Skeleton'
import { useNow } from '../lib/useNow'
import { errorMessage, useRead } from '../lib/useRead'
import { useVisibleInterval } from '../lib/useVisibleInterval'
import ActivityRow from './activity/ActivityRow'
import { dateOf, dayLabel, KINDS, type Kind, parseFilters, serverToday } from './activity/filters'

/** REFRESH_MS is how often the first page is read again while the tab is visible. */
const REFRESH_MS = 30_000

type State = { rows: ActivityEntry[]; more: boolean; loaded: boolean; error: string }
const EMPTY: State = { rows: [], more: false, loaded: false, error: '' }

/**
 * useActivity is the timeline (spec decisions 24 and 25): the newest page
 * under the filters, older pages on demand. The newest page is read again by
 * `refresh` (the page calls it every 30 s) only while no older page is loaded:
 * after Show older the list stays as it is until the filters change. A failed
 * read keeps the rows and reports the error.
 */
function useActivity(kind: Kind | undefined, project: number | undefined) {
  const [state, setState] = useState<State>(EMPTY)
  const [older, setOlder] = useState({ busy: false, error: '' })
  const newestCtl = useRef<AbortController | null>(null)
  const olderCtl = useRef<AbortController | null>(null)
  const olderLoaded = useRef(false)

  const readNewest = useCallback(() => {
    newestCtl.current?.abort()
    const ctl = new AbortController()
    newestCtl.current = ctl
    getActivity({ kind, project }, ctl.signal).then(
      (page) => {
        if (ctl.signal.aborted || olderLoaded.current) return
        setState({ rows: page.entries, more: page.more, loaded: true, error: '' })
      },
      (err: unknown) => {
        if (ctl.signal.aborted) return
        setState((prev) => ({ ...prev, loaded: true, error: errorMessage(err) }))
      },
    )
  }, [kind, project])

  useEffect(() => {
    olderCtl.current?.abort()
    olderCtl.current = null
    olderLoaded.current = false
    setState(EMPTY)
    setOlder({ busy: false, error: '' })
    readNewest()
    return () => {
      newestCtl.current?.abort()
      olderCtl.current?.abort()
    }
  }, [readNewest])

  const refresh = useCallback(() => {
    if (!olderLoaded.current) readNewest()
  }, [readNewest])

  const readOlder = () => {
    const last = state.rows.at(-1)
    if (olderCtl.current !== null || last === undefined) return
    // A refresh still on its way must not replace what this page adds.
    newestCtl.current?.abort()
    const ctl = new AbortController()
    olderCtl.current = ctl
    setOlder({ busy: true, error: '' })
    getActivity({ kind, project, before: last.id }, ctl.signal).then(
      (page) => {
        if (ctl.signal.aborted) return
        olderCtl.current = null
        olderLoaded.current = true
        setState((prev) => {
          const end = prev.rows.at(-1)?.id ?? Number.POSITIVE_INFINITY
          return {
            ...prev,
            rows: [...prev.rows, ...page.entries.filter((e) => e.id < end)],
            more: page.more,
          }
        })
        setOlder({ busy: false, error: '' })
      },
      (err: unknown) => {
        if (ctl.signal.aborted) return
        olderCtl.current = null
        setOlder({ busy: false, error: errorMessage(err) })
      },
    )
  }

  return { ...state, older, readNewest, refresh, readOlder }
}

/** Day is a run of records of one server-zone date. */
type Day = { date: string; label: string; entries: ActivityEntry[] }

/** daysOf groups the records, which come newest first, into consecutive days. */
function daysOf(rows: ActivityEntry[], today: string): Day[] {
  const days: Day[] = []
  for (const e of rows) {
    const date = dateOf(e.at)
    const last = days.at(-1)
    if (last !== undefined && last.date === date) last.entries.push(e)
    else days.push({ date, label: dayLabel(e.at, today), entries: [e] })
  }
  return days
}

const CHIP =
  'h-7 cursor-pointer rounded-md border-0 px-3 font-sans text-[13px] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent'
const FIELD =
  'flex h-9 items-center gap-2 rounded-[7px] border border-border-strong bg-surface px-2.5 text-[13px] text-muted focus-within:outline-2 focus-within:outline-offset-2 focus-within:outline-accent'

/** KindChips and ProjectSelect are design 06's filters; they write the URL query. */
function Filters({
  kind,
  project,
  projects,
  set,
}: {
  kind: Kind | undefined
  project: number | undefined
  projects: Project[]
  set(key: 'kind' | 'project', value: string): void
}) {
  const selectId = useId()
  const sorted = [...projects].sort((a, b) => a.displayName.localeCompare(b.displayName))
  const unknown = project !== undefined && !sorted.some((p) => p.id === project)
  const chips: [Kind | '', string][] = [['', 'All'], ...KINDS]
  return (
    <>
      <fieldset className="m-0 flex rounded-lg border border-border-strong bg-surface p-[3px]">
        <legend className="sr-only">Filter by event type</legend>
        {chips.map(([value, label]) => {
          const on = (kind ?? '') === value
          return (
            <button
              key={label}
              type="button"
              aria-pressed={on}
              onClick={() => set('kind', value)}
              className={`${CHIP} ${on ? 'bg-selected text-white' : 'bg-transparent text-muted'}`}
            >
              {label}
            </button>
          )
        })}
      </fieldset>
      <label htmlFor={selectId} className={FIELD}>
        Project
        <select
          id={selectId}
          value={project === undefined ? '' : String(project)}
          onChange={(e) => set('project', e.target.value)}
          className="cursor-pointer border-0 bg-transparent font-sans text-[13px] text-text outline-none [&>option]:bg-surface"
        >
          <option value="">All</option>
          {unknown && <option value={String(project)}>{`Project ${project}`}</option>}
          {sorted.map((p) => (
            <option key={p.id} value={String(p.id)}>
              {p.displayName}
            </option>
          ))}
        </select>
      </label>
    </>
  )
}

/**
 * ActivityPage is /activity (design 06, spec decisions 20 to 25): who did
 * what, on which project, and the outcome, plus the system's alerts, by day
 * in the server zone, filtered by the URL query and read 50 at a time.
 */
export default function ActivityPage() {
  const live = useLive()
  const [params, setParams] = useSearchParams()
  const { kind, project } = parseFilters(params)
  const timeline = useActivity(kind, project)
  const zone = useRead(getBackupSettings, 'zone').data?.timeZone ?? null
  const now = useNow(60_000)
  useVisibleInterval(timeline.refresh, REFRESH_MS)

  const filtered = kind !== undefined || project !== undefined
  const set = (key: 'kind' | 'project', value: string) => {
    const next = new URLSearchParams(params)
    if (value === '') next.delete(key)
    else next.set(key, value)
    setParams(next, { replace: true })
  }
  const clear = () => {
    const next = new URLSearchParams(params)
    next.delete('kind')
    next.delete('project')
    setParams(next, { replace: true })
  }

  const days = daysOf(timeline.rows, serverToday(now, zone, timeline.rows[0]?.at))
  let body: ReactNode
  if (timeline.error !== '' && timeline.rows.length === 0) {
    body = (
      <LoadError
        title="Could not load the activity."
        message={timeline.error}
        onRetry={timeline.readNewest}
      />
    )
  } else if (!timeline.loaded) {
    body = <SkeletonCard />
  } else if (timeline.rows.length === 0) {
    body = filtered ? (
      <EmptyState
        icon={SearchX}
        title="Nothing matches these filters."
        action={<EmptyStateButton onClick={clear}>Clear filters</EmptyStateButton>}
      />
    ) : (
      <EmptyState icon={Activity} title="No activity yet." />
    )
  } else {
    body = (
      <>
        {timeline.error !== '' && (
          <LoadError
            title="Could not load the activity."
            message={timeline.error}
            onRetry={timeline.readNewest}
          />
        )}
        <div
          data-testid="activity-days"
          className={`flex flex-col gap-5 ${timeline.error !== '' ? 'opacity-55 saturate-[0.6]' : ''}`}
        >
          {days.map((d) => (
            <DaySection key={d.date} day={d} />
          ))}
        </div>
        {timeline.more && (
          <div className="flex flex-wrap items-center gap-3">
            <button
              type="button"
              disabled={timeline.older.busy}
              aria-busy={timeline.older.busy || undefined}
              onClick={timeline.readOlder}
              className="h-8 cursor-pointer rounded-md border border-border-strong bg-surface-2 px-3 font-sans text-[13px] text-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-60"
            >
              Show older
            </button>
            {timeline.older.error !== '' && (
              <span className="text-[13px] text-danger">{timeline.older.error}</span>
            )}
          </div>
        )}
      </>
    )
  }

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Activity"
        subtitle="Who did what, on which project, and the outcome · plus system alerts"
        actions={<Filters kind={kind} project={project} projects={live.projects ?? []} set={set} />}
      />
      <LiveDim className="flex flex-col gap-5">{body}</LiveDim>
    </div>
  )
}

/** DaySection is one day's heading and its records (design 06). */
function DaySection({ day }: { day: Day }) {
  const id = useId()
  return (
    <section className="flex flex-col">
      <h2
        id={id}
        className="m-0 mb-2.5 text-[12.5px] font-semibold tracking-[0.06em] text-muted uppercase"
      >
        {day.label}
      </h2>
      <ol
        aria-labelledby={id}
        className="m-0 list-none overflow-hidden rounded-[10px] border border-border bg-surface p-0"
      >
        {day.entries.map((e, i) => (
          <ActivityRow key={e.id} entry={e} first={i === 0} />
        ))}
      </ol>
    </section>
  )
}
