import { CloudUpload, RotateCw } from 'lucide-react'
import { useCallback, useEffect, useId, useRef, useState } from 'react'
import { downloadUrl, listBackups } from '../../api/backups'
import type { Backup, BackupHistory as Page, Project } from '../../api/types.gen'
import { useLive } from '../../app/live'
import LoadError from '../../components/LoadError'
import { backupLine, isActive, isRetryable } from '../../lib/backup'
import { formatBytes, formatDuration } from '../../lib/format'
import { formatWhen } from '../../lib/time'
import type { BackupFlow } from '../../lib/useBackup'
import BackUpButton from './BackUpButton'

const HEAD = 'px-3 py-2.5 font-normal first:pl-4 last:pr-4'
const CELL = 'px-3 py-2.5 align-middle first:pl-4 last:pr-4'
const ROW_BUTTON =
  'h-7 cursor-pointer rounded-md border border-border-strong bg-transparent px-2.5 font-sans text-[12.5px] text-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-60'
const LINK =
  'font-sans text-[13px] text-accent no-underline hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent'

/** The colour of a status, from designs 02 and 04: text, then its dot. */
const TONE: Record<string, [string, string]> = {
  ok: ['text-success', 'bg-success'],
  failed: ['text-[#ff8c8f]', 'bg-[#ff6b6e]'],
  refused: ['text-[#ff8c8f]', 'bg-[#ff6b6e]'],
  interrupted: ['text-warning', 'bg-warning'],
  running: ['text-[#7fb8ff]', 'bg-accent'],
  queued: ['text-[#7fb8ff]', 'bg-accent'],
}

const message = (err: unknown) =>
  err instanceof Error && err.message ? err.message : 'Something went wrong.'

/** byNewest sorts backups newest first (ids grow with time). */
const byNewest = (a: Backup, b: Backup) => b.id - a.id

/**
 * mergeNewest puts a fresh newest page in front of what was shown: rows
 * older than the page are kept (with their `more`), so a re-read never drops
 * the older pages the Operator opened.
 */
function mergeNewest(rows: Backup[], more: boolean, page: Page): { rows: Backup[]; more: boolean } {
  const last = page.backups.at(-1)
  if (last === undefined) return { rows: [], more: false }
  const tail = rows.filter((b) => b.id < last.id)
  return tail.length > 0
    ? { rows: [...page.backups, ...tail], more }
    : { rows: page.backups, more: page.more }
}

/** upsert replaces or adds backups by id, newest first. */
function upsert(rows: Backup[], add: Backup[]): Backup[] {
  const ids = new Set(add.map((b) => b.id))
  return [...rows.filter((b) => !ids.has(b.id)), ...add].sort(byNewest)
}

type State = { rows: Backup[]; more: boolean; loaded: boolean; error: string }
const EMPTY: State = { rows: [], more: false, loaded: false, error: '' }

/**
 * useHistory is a project's backup history: the newest page on mount, older
 * pages on demand, and one re-read of the newest page each time a live state
 * shows that active backups of this project finished, or the flow's refusals
 * count says a refused backup was stored. Until that answers, a finished
 * backup keeps its last live value.
 */
function useHistory(projectId: number, refusals: number) {
  const live = useLive()
  const [state, setState] = useState<State>(EMPTY)
  const [older, setOlder] = useState({ busy: false, error: '' })
  const newestCtl = useRef<AbortController | null>(null)
  const olderCtl = useRef<AbortController | null>(null)

  const readNewest = useCallback(() => {
    newestCtl.current?.abort()
    const ctl = new AbortController()
    newestCtl.current = ctl
    listBackups(projectId, undefined, ctl.signal).then(
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
        setState((prev) => ({ ...prev, error: message(err) }))
      },
    )
  }, [projectId])

  useEffect(() => {
    setState(EMPTY)
    setOlder({ busy: false, error: '' })
    readNewest()
    return () => {
      newestCtl.current?.abort()
      olderCtl.current?.abort()
    }
  }, [readNewest])

  const readOlder = (before: number) => {
    if (olderCtl.current !== null) return
    const ctl = new AbortController()
    olderCtl.current = ctl
    setOlder({ busy: true, error: '' })
    listBackups(projectId, before, ctl.signal).then(
      (page) => {
        if (ctl.signal.aborted) return
        olderCtl.current = null
        setState((prev) => {
          const last = prev.rows.at(-1)?.id ?? Number.POSITIVE_INFINITY
          return {
            ...prev,
            rows: [...prev.rows, ...page.backups.filter((b) => b.id < last)],
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

  // A refusal that stored a refused backup: it never was active, so read it now.
  const lastRefusals = useRef(refusals)
  useEffect(() => {
    if (lastRefusals.current === refusals) return
    lastRefusals.current = refusals
    readNewest()
  }, [refusals, readNewest])

  // The active backups of this project at the last live state, to see which finished.
  const seen = useRef(new Map<number, Backup>())
  useEffect(() => {
    const now = new Map<number, Backup>()
    for (const b of live.backups) if (b.projectId === projectId) now.set(b.id, b)
    const gone = [...seen.current.values()].filter(
      (b) => b.projectId === projectId && !now.has(b.id),
    )
    seen.current = now
    if (gone.length === 0) return
    setState((prev) => ({ ...prev, rows: upsert(prev.rows, gone) }))
    readNewest()
  }, [live.backups, projectId, readNewest])

  // What the table shows: the stored rows with the live value of an active
  // one, and active backups the list has not read yet.
  const active = live.backups.filter((b) => b.projectId === projectId)
  const liveById = new Map(active.map((b) => [b.id, b]))
  const known = new Set(state.rows.map((b) => b.id))
  const rows = [
    ...active.filter((b) => !known.has(b.id)),
    ...state.rows.map((b) => liveById.get(b.id) ?? b),
  ].sort(byNewest)

  return { ...state, rows, older, readNewest, readOlder }
}

type Props = {
  /** The project as the server last sent it (what the flow takes). */
  project: Project
  flow: BackupFlow
}

/**
 * BackupHistory is the Backup history card of design 02: the project's
 * backups, newest first, 50 at a time with Show older, each with its status
 * words and Open in Drive and Download, or Retry. A row's status opens its
 * progress dialog.
 */
export default function BackupHistory({ project, flow }: Props) {
  const titleId = useId()
  const h = useHistory(project.id, flow.refusals)
  const several = project.databases.length > 1

  return (
    <section aria-labelledby={titleId} className="rounded-[10px] border border-border bg-surface">
      <div className="flex items-center justify-between border-b border-border px-4 py-3.5">
        <h2 id={titleId} className="m-0 text-[15px] font-semibold">
          Backup history
        </h2>
      </div>

      {h.error !== '' && (
        <div className="px-4 py-3.5">
          <LoadError
            title="Could not load the backup history."
            message={h.error}
            onRetry={h.readNewest}
          />
        </div>
      )}

      {!h.loaded ? (
        h.error === '' && (
          <p role="status" className="m-0 px-4 py-5 text-[13px] text-muted">
            Loading backups…
          </p>
        )
      ) : h.rows.length === 0 ? (
        <NoBackups project={project} flow={flow} />
      ) : (
        <>
          <div className="overflow-x-auto">
            <table
              aria-labelledby={titleId}
              className="w-full min-w-[640px] border-collapse text-left"
            >
              <thead>
                <tr className="border-b border-border text-xs tracking-[0.05em] text-muted uppercase">
                  <th scope="col" className={HEAD}>
                    Time
                  </th>
                  {several && (
                    <th scope="col" className={HEAD}>
                      Database
                    </th>
                  )}
                  <th scope="col" className={HEAD}>
                    Size
                  </th>
                  <th scope="col" className={HEAD}>
                    Duration
                  </th>
                  <th scope="col" className={HEAD}>
                    Status
                  </th>
                  <th scope="col" className={HEAD}>
                    Triggered by
                  </th>
                  <th scope="col" className={`${HEAD} text-right`}>
                    File
                  </th>
                </tr>
              </thead>
              <tbody className="font-mono text-[13px] tabular-nums">
                {h.rows.map((b) => (
                  <Row key={b.id} backup={b} several={several} flow={flow} />
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
                onClick={() => {
                  const last = h.rows.at(-1)
                  if (last) h.readOlder(last.id)
                }}
                className={`${ROW_BUTTON} h-8 px-3 text-[13px]`}
              >
                Show older
              </button>
              {h.older.error !== '' && (
                <span className="text-[13px] text-danger">{h.older.error}</span>
              )}
            </div>
          )}
        </>
      )}
    </section>
  )
}

function Row({ backup: b, several, flow }: { backup: Backup; several: boolean; flow: BackupFlow }) {
  const [text, dot] = TONE[b.state] ?? TONE.queued
  const retrying = flow.retryingId === b.id
  return (
    <tr data-id={b.id} className="border-b border-[#1a1e24] last:border-b-0">
      <td className={CELL}>{formatWhen(b.createdAt)}</td>
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
      <td className={`${CELL} font-sans break-all text-[#c9ced6]`}>{b.startedBy || '—'}</td>
      {/* w-px: the actions take their whole width; Time and Triggered by wrap instead. */}
      <td className={`${CELL} w-px whitespace-nowrap`}>
        <span className="flex items-center justify-end gap-3 whitespace-nowrap">
          {b.state === 'ok' && (
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
              className={`${ROW_BUTTON} flex items-center gap-1.5`}
            >
              <RotateCw aria-hidden="true" size={13} strokeWidth={2} />
              Retry
            </button>
          )}
        </span>
      </td>
    </tr>
  )
}

/** NoBackups is the empty history: "No backup yet." and Back up now, per database when there are several. */
function NoBackups({ project, flow }: Props) {
  const dbs = project.databases
  return (
    <div className="flex flex-col items-center gap-3 px-5 py-8 text-center">
      <CloudUpload aria-hidden="true" size={30} strokeWidth={1.4} className="text-[#4a515c]" />
      <p className="m-0 text-[13px] text-text-2">No backup yet.</p>
      {dbs.length === 1 && <BackUpButton project={project} db={dbs[0]} flow={flow} />}
      {dbs.length > 1 && (
        <ul className="m-0 flex w-full max-w-[420px] list-none flex-col gap-2 p-0">
          {dbs.map((d) => (
            <li
              key={d.key}
              className="flex items-center justify-between gap-3 rounded-lg border border-border bg-[#0f1215] px-3 py-2"
            >
              <span className="min-w-0 font-mono text-[13px] break-all">{d.name}</span>
              <BackUpButton project={project} db={d} flow={flow} />
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
