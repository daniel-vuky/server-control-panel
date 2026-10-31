import { useCallback, useEffect, useId, useRef, useState } from 'react'
import { type BackupQuery, listBackups } from '../../api/backups'
import type { Backup, Project } from '../../api/types.gen'
import { useLive } from '../../app/live'
import LoadError from '../../components/LoadError'
import type { BackupFlow } from '../../lib/useBackup'
import { BackupRow, HEAD, mergeNewest, ROW_BUTTON } from '../project/BackupHistory'

/** Filters are the table's filters, as the URL query holds them. */
export type Filters = {
  project: number | null
  result: '' | 'ok' | 'failed'
  from: string
  to: string
}

const DATE = /^\d{4}-\d{2}-\d{2}$/

/** filtersOf reads the filters from a URL query; a value that is not valid is left out. */
export function filtersOf(params: URLSearchParams): Filters {
  const project = Number(params.get('project'))
  const result = params.get('result')
  const date = (key: string) => {
    const v = params.get(key) ?? ''
    return DATE.test(v) ? v : ''
  }
  return {
    project: Number.isInteger(project) && project > 0 ? project : null,
    result: result === 'ok' || result === 'failed' ? result : '',
    from: date('from'),
    to: date('to'),
  }
}

/** isFiltered says whether any filter is set. */
export const isFiltered = (f: Filters): boolean =>
  f.project !== null || f.result !== '' || f.from !== '' || f.to !== ''

const queryOf = (f: Filters): BackupQuery => ({
  project: f.project ?? undefined,
  result: f.result || undefined,
  from: f.from || undefined,
  to: f.to || undefined,
})

/** matches says whether an active backup belongs in the table under these filters. */
const matches = (b: Backup, f: Filters): boolean =>
  f.result === '' && f.to === '' && (f.project === null || b.projectId === f.project)

const byNewest = (a: Backup, b: Backup) => b.id - a.id

const message = (err: unknown) =>
  err instanceof Error && err.message ? err.message : 'Something went wrong.'

type State = { rows: Backup[]; more: boolean; loaded: boolean; error: string }
const EMPTY: State = { rows: [], more: false, loaded: false, error: '' }

/**
 * useAllBackups is the Backups page's table (spec decision 21): the newest
 * page under the filters, older pages on demand, and the newest page read
 * again each time `changed` changes (an active backup came or went, the
 * batch moved, a refusal was stored). Active backups show their live value;
 * ones the list has not read yet are added while no filter would hide them.
 */
export function useAllBackups(filters: Filters, changed: string) {
  const live = useLive()
  const [state, setState] = useState<State>(EMPTY)
  const [older, setOlder] = useState({ busy: false, error: '' })
  const newestCtl = useRef<AbortController | null>(null)
  const olderCtl = useRef<AbortController | null>(null)
  const { project, result, from, to } = filters

  const readNewest = useCallback(() => {
    newestCtl.current?.abort()
    const ctl = new AbortController()
    newestCtl.current = ctl
    listBackups(queryOf({ project, result, from, to }), ctl.signal).then(
      (page) => {
        if (ctl.signal.aborted) return
        setState((prev) => ({
          ...mergeNewest(prev.rows, prev.more, page),
          loaded: true,
          error: '',
        }))
      },
      (err: unknown) => {
        if (ctl.signal.aborted) return
        setState((prev) => ({ ...prev, loaded: true, error: message(err) }))
      },
    )
  }, [project, result, from, to])

  useEffect(() => {
    olderCtl.current?.abort()
    olderCtl.current = null
    setState(EMPTY)
    setOlder({ busy: false, error: '' })
    readNewest()
    return () => {
      newestCtl.current?.abort()
      olderCtl.current?.abort()
    }
  }, [readNewest])

  // Something changed in the live state: read the newest page again.
  const lastChanged = useRef(changed)
  useEffect(() => {
    if (lastChanged.current === changed) return
    lastChanged.current = changed
    readNewest()
  }, [changed, readNewest])

  // An active backup that ended keeps its last live value until the re-read answers.
  const filtersRef = useRef(filters)
  filtersRef.current = filters
  const seen = useRef(new Map<number, Backup>())
  useEffect(() => {
    const now = new Map(live.backups.map((b) => [b.id, b]))
    const gone = new Map([...seen.current].filter(([id]) => !now.has(id)))
    seen.current = now
    if (gone.size === 0) return
    setState((prev) => {
      const rows = prev.rows.map((b) => gone.get(b.id) ?? b)
      const known = new Set(rows.map((b) => b.id))
      const add = [...gone.values()].filter(
        (b) => !known.has(b.id) && matches(b, filtersRef.current),
      )
      return { ...prev, rows: [...add, ...rows].sort(byNewest) }
    })
  }, [live.backups])

  const readOlder = () => {
    const last = state.rows.at(-1)
    if (olderCtl.current !== null || last === undefined) return
    const ctl = new AbortController()
    olderCtl.current = ctl
    setOlder({ busy: true, error: '' })
    listBackups({ ...queryOf({ project, result, from, to }), before: last.id }, ctl.signal).then(
      (page) => {
        if (ctl.signal.aborted) return
        olderCtl.current = null
        setState((prev) => {
          const end = prev.rows.at(-1)?.id ?? Number.POSITIVE_INFINITY
          return {
            ...prev,
            rows: [...prev.rows, ...page.backups.filter((b) => b.id < end)],
            more: page.more,
          }
        })
        setOlder({ busy: false, error: '' })
      },
      (err: unknown) => {
        if (ctl.signal.aborted) return
        olderCtl.current = null
        setOlder({ busy: false, error: message(err) })
      },
    )
  }

  const liveById = new Map(live.backups.map((b) => [b.id, b]))
  const known = new Set(state.rows.map((b) => b.id))
  const rows = [
    ...live.backups.filter((b) => !known.has(b.id) && matches(b, filters)),
    ...state.rows.map((b) => liveById.get(b.id) ?? b),
  ].sort(byNewest)

  return { ...state, rows, older, readNewest, readOlder }
}

export type AllBackups = ReturnType<typeof useAllBackups>

const FIELD =
  'flex h-[34px] items-center gap-2 rounded-[7px] border border-border-strong bg-surface px-2.5 text-[13px] text-muted focus-within:outline-2 focus-within:outline-offset-2 focus-within:outline-accent'
const INPUT = 'border-0 bg-transparent font-sans text-[13px] text-text outline-none'
const CHIPS: [Filters['result'], string][] = [
  ['', 'All'],
  ['ok', 'Succeeded'],
  ['failed', 'Failed'],
]

type FilterProps = {
  filters: Filters
  projects: Project[]
  /** set writes one filter into the URL; '' removes it. */
  set(key: 'project' | 'result' | 'from' | 'to', value: string): void
}

/** BackupFilters are design 04's filters: Project, All / Succeeded / Failed, From and To. */
export function BackupFilters({ filters: f, projects, set }: FilterProps) {
  const projectId = useId()
  const fromId = useId()
  const toId = useId()
  const sorted = [...projects].sort((a, b) => a.displayName.localeCompare(b.displayName))
  const unknown = f.project !== null && !sorted.some((p) => p.id === f.project)
  return (
    <div className="flex flex-wrap items-center gap-2.5">
      <label htmlFor={projectId} className={FIELD}>
        Project
        <select
          id={projectId}
          value={f.project === null ? '' : String(f.project)}
          onChange={(e) => set('project', e.target.value)}
          className={`${INPUT} cursor-pointer [&>option]:bg-surface`}
        >
          <option value="">All</option>
          {unknown && <option value={String(f.project)}>{`Project ${f.project}`}</option>}
          {sorted.map((p) => (
            <option key={p.id} value={String(p.id)}>
              {p.displayName}
            </option>
          ))}
        </select>
      </label>
      <fieldset className="m-0 flex rounded-lg border border-border-strong bg-surface p-[3px]">
        <legend className="sr-only">Filter by status</legend>
        {CHIPS.map(([value, label]) => {
          const on = f.result === value
          return (
            <button
              key={label}
              type="button"
              aria-pressed={on}
              onClick={() => set('result', value)}
              className={`h-7 cursor-pointer rounded-md border-0 px-3 font-sans text-[13px] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent ${on ? 'bg-[#262c35] text-white' : 'bg-transparent text-muted'}`}
            >
              {label}
            </button>
          )
        })}
      </fieldset>
      <label htmlFor={fromId} className={FIELD}>
        From
        <input
          id={fromId}
          type="date"
          value={f.from}
          max={f.to || undefined}
          onChange={(e) => set('from', e.target.value)}
          className={`${INPUT} [color-scheme:dark]`}
        />
      </label>
      <label htmlFor={toId} className={FIELD}>
        To
        <input
          id={toId}
          type="date"
          value={f.to}
          min={f.from || undefined}
          onChange={(e) => set('to', e.target.value)}
          className={`${INPUT} [color-scheme:dark]`}
        />
      </label>
    </div>
  )
}

type TableProps = {
  all: AllBackups
  flow: BackupFlow
  /** The tracked projects, for the number of copies a database keeps. */
  projects: Project[]
}

/**
 * BackupsTable is design 04's table of every backup, newest first, 50 at a
 * time with Show older. Its rows are the project history's rows with a
 * Project column.
 */
export default function BackupsTable({ all: h, flow, projects }: TableProps) {
  const keepOf = (b: Backup) =>
    projects.find((p) => p.id === b.projectId)?.databases.find((d) => d.key === b.database.key)
      ?.schedule?.keep ?? null

  if (h.error !== '' && h.rows.length === 0) {
    return (
      <LoadError title="Could not load the backups." message={h.error} onRetry={h.readNewest} />
    )
  }
  if (!h.loaded) {
    return (
      <p role="status" className="m-0 text-[13px] text-muted">
        Loading backups…
      </p>
    )
  }
  if (h.rows.length === 0) {
    return (
      <p className="m-0 rounded-[10px] border border-border bg-surface px-4 py-5 text-[13px] text-muted">
        No backups match these filters.
      </p>
    )
  }
  return (
    <section className="rounded-[10px] border border-border bg-surface">
      {h.error !== '' && (
        <div className="px-4 py-3.5">
          <LoadError title="Could not load the backups." message={h.error} onRetry={h.readNewest} />
        </div>
      )}
      <div className="overflow-x-auto">
        <table aria-label="All backups" className="w-full min-w-[980px] border-collapse text-left">
          <thead>
            <tr className="border-b border-border text-xs tracking-[0.05em] text-muted uppercase">
              {['Time', 'Project', 'Database', 'Size', 'Duration', 'Status', 'Triggered by'].map(
                (name) => (
                  <th key={name} scope="col" className={HEAD}>
                    {name}
                  </th>
                ),
              )}
              <th scope="col" className={`${HEAD} text-right`}>
                File
              </th>
            </tr>
          </thead>
          <tbody className="font-mono text-[13px] tabular-nums">
            {h.rows.map((b) => (
              <BackupRow key={b.id} backup={b} several withProject flow={flow} keep={keepOf(b)} />
            ))}
          </tbody>
        </table>
      </div>
      {h.more && (
        <div className="flex flex-wrap items-center gap-3 border-t border-border px-4 py-3">
          <button
            type="button"
            disabled={h.older.busy}
            aria-busy={h.older.busy || undefined}
            onClick={h.readOlder}
            className={`${ROW_BUTTON} h-8 px-3 text-[13px]`}
          >
            Show older
          </button>
          {h.older.error !== '' && <span className="text-[13px] text-danger">{h.older.error}</span>}
        </div>
      )}
    </section>
  )
}
