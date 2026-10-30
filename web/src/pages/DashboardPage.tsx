import { FolderPlus, RotateCw } from 'lucide-react'
import { useEffect, useLayoutEffect, useRef } from 'react'
import { Link, useNavigate } from 'react-router'
import { listProjects } from '../api/projects'
import type { Project, Workspace } from '../api/types.gen'
import { listWorkspaces } from '../api/workspaces'
import { useLive } from '../app/live'
import ActionMenu from '../components/ActionMenu'
import DatabaseCell from '../components/DatabaseCell'
import LoadError from '../components/LoadError'
import MonitorOnlyTag from '../components/MonitorOnlyTag'
import ProjectSwitch from '../components/ProjectSwitch'
import StatusBadge from '../components/StatusBadge'
import { formatCpu, formatMem } from '../lib/format'
import type { Status } from '../lib/status'
import { formatLastScan } from '../lib/time'
import { useLoad } from '../lib/useLoad'
import { useProjectActions } from '../lib/useProjectActions'
import { useRescan } from '../lib/useRescan'

const SCROLL_KEY = 'dashboard.scrollY'

/**
 * useScrollMemory saves the page scroll when the Dashboard is left and puts it
 * back once the rows are there to scroll through. Storage can throw (private
 * windows), and then the page just opens at the top.
 */
function useScrollMemory(ready: boolean) {
  // A visit that ends before the rows came has nothing worth saving.
  const shown = useRef(false)
  useLayoutEffect(() => {
    if (!ready) return
    shown.current = true
    try {
      const saved = Number(sessionStorage.getItem(SCROLL_KEY))
      if (saved > 0) window.scrollTo(0, saved)
    } catch {
      // No storage: nothing to restore.
    }
  }, [ready])

  useEffect(
    () => () => {
      if (!shown.current) return
      try {
        sessionStorage.setItem(SCROLL_KEY, String(window.scrollY))
      } catch {
        // No storage: the next visit opens at the top.
      }
    },
    [],
  )
}

/** DashboardPage is /: the tracked projects as a table (cards come with E3-F08). */
export default function DashboardPage() {
  const { load, retry } = useLoad(listProjects)
  const live = useLive()
  const actions = useProjectActions()
  const navigate = useNavigate()
  const workspaces = useLoad(listWorkspaces).load

  // No workspace is the first screen: setup. A failed load shows the Dashboard
  // as it is, so there is no redirect loop.
  const noWorkspace = workspaces.state === 'ok' && workspaces.data.length === 0
  useEffect(() => {
    if (noWorkspace) navigate('/onboarding', { replace: true })
  }, [noWorkspace, navigate])

  const dockerDown = live.dockerUp === false
  const base = live.projects ?? (load.state === 'ok' ? load.data : null)
  const rows = base?.filter((p) => !actions.removedIds.has(p.id)).map(actions.overlay)

  useScrollMemory(rows !== undefined && rows.length > 0)

  return (
    <section className="flex max-w-[1040px] flex-col gap-[18px]">
      <Header workspaces={workspaces.state === 'ok' ? workspaces.data : null} />

      {rows === undefined && load.state === 'loading' && <Loading />}
      {rows === undefined && load.state === 'error' && (
        <LoadError title="Could not load projects." message={load.message} onRetry={retry} />
      )}
      {rows !== undefined &&
        (rows.length === 0 ? (
          <Empty />
        ) : (
          <ProjectTable
            projects={rows}
            dockerDown={dockerDown}
            onStart={actions.start}
            onStop={actions.stop}
            onRestart={actions.restart}
            onUntrack={actions.leave}
          />
        ))}

      {actions.dialogs}
    </section>
  )
}

/** latestScan is the newest lastScanAt of the workspaces, or "" when none was ever scanned. */
function latestScan(workspaces: Workspace[]): string {
  let best = ''
  let bestAt = Number.NEGATIVE_INFINITY
  for (const w of workspaces) {
    const at = Date.parse(w.lastScanAt)
    if (!Number.isNaN(at) && at > bestAt) {
      best = w.lastScanAt
      bestAt = at
    }
  }
  return best
}

/**
 * Header is the title, the workspace line of design 01 and the Rescan button.
 * Without the workspaces (loading, failed or none) it keeps the plain subtitle
 * and no button.
 */
function Header({ workspaces }: { workspaces: Workspace[] | null }) {
  const { starting, rescan } = useRescan()
  const known = workspaces !== null && workspaces.length > 0

  return (
    <header className="flex flex-wrap items-end justify-between gap-4">
      <div className="flex flex-col gap-1.5">
        <h1 className="text-2xl font-semibold tracking-tight">Dashboard</h1>
        <p className="text-[13px] text-muted">
          {known ? (
            <>
              {workspaces.length === 1 ? (
                <>
                  Workspace <span className="font-mono text-text-2">{workspaces[0].path}</span>
                </>
              ) : (
                `${workspaces.length} workspaces`
              )}{' '}
              · last scanned {formatLastScan(latestScan(workspaces))}
            </>
          ) : (
            'Tracked projects and their status'
          )}
        </p>
      </div>
      {known && (
        <button
          type="button"
          disabled={starting}
          onClick={() => void rescan()}
          className="flex h-9 cursor-pointer items-center gap-2 rounded-[7px] border border-border-strong bg-surface-2 px-3.5 text-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-60"
        >
          <RotateCw aria-hidden="true" size={16} strokeWidth={1.8} />
          Rescan
        </button>
      )}
    </header>
  )
}

function Loading() {
  return (
    <div role="status" className="flex flex-col gap-2">
      <span className="sr-only">Loading projects…</span>
      {[0, 1, 2].map((i) => (
        <div
          key={i}
          aria-hidden="true"
          className="h-12 animate-pulse rounded-lg border border-border bg-surface"
        />
      ))}
    </div>
  )
}

function Empty() {
  return (
    <div className="flex min-h-[220px] flex-col items-center justify-center gap-2.5 rounded-xl border border-dashed border-border-strong p-5 text-center">
      <FolderPlus aria-hidden="true" size={28} strokeWidth={1.6} className="text-muted" />
      <span className="font-semibold">No projects yet</span>
      <span className="text-[13px] leading-normal text-muted">
        Add a workspace and the panel will find the compose projects in it.
      </span>
      <Link
        to="/onboarding"
        className="flex h-9 items-center rounded-[7px] border border-border-strong px-3.5 text-text"
      >
        Add workspace
      </Link>
    </div>
  )
}

type TableProps = {
  projects: Project[]
  dockerDown: boolean
  onStart(p: Project): void
  onStop(p: Project): void
  onRestart(p: Project): void
  onUntrack(p: Project): void
}

/** RAM_RANGE is the full RAM bar of design 01: 4 GB. */
const RAM_RANGE = 4 * 1024 * 1024 * 1024

/** Meter is a live value with the thin bar of design 01; with no value there is no bar. */
function Meter({
  label,
  fraction,
  color,
  testId,
}: {
  label: string
  fraction: number | null
  color: string
  testId: string
}) {
  return (
    <span className="flex w-[84px] flex-col gap-1.5">
      <span className="font-mono text-[13px] tabular-nums">{label}</span>
      {fraction !== null && (
        <span aria-hidden="true" className="flex h-[3px] rounded-[2px] bg-border">
          <span
            data-testid={testId}
            className="rounded-[2px]"
            style={{ width: `${Math.min(Math.max(fraction, 0), 1) * 100}%`, background: color }}
          />
        </span>
      )}
    </span>
  )
}

function ProjectTable({ projects, dockerDown, onStart, onStop, onRestart, onUntrack }: TableProps) {
  return (
    <div className="overflow-x-auto rounded-[10px] border border-border bg-surface">
      <table className="w-full min-w-[1000px] border-collapse text-left">
        <thead>
          <tr className="border-b border-border text-xs tracking-wider text-muted uppercase">
            <th scope="col" className="px-4 py-2.5 font-normal">
              Display name
            </th>
            <th scope="col" className="px-3 py-2.5 font-normal">
              Path
            </th>
            <th scope="col" className="px-3 py-2.5 font-normal">
              Status
            </th>
            <th scope="col" className="px-3 py-2.5 font-normal">
              CPU
            </th>
            <th scope="col" className="px-3 py-2.5 font-normal">
              RAM
            </th>
            <th scope="col" className="px-3 py-2.5 font-normal">
              Database
            </th>
            <th scope="col" className="py-2.5 pr-4 pl-3 text-right font-normal">
              Start / Stop
            </th>
          </tr>
        </thead>
        <tbody>
          {projects.map((p) => (
            <tr key={p.id} className="border-b border-border align-top last:border-b-0">
              <td className="px-4 py-3">
                <span className="flex items-center gap-2">
                  <Link
                    to={`/projects/${p.id}`}
                    className="font-mono font-semibold text-text no-underline hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
                  >
                    {p.displayName}
                  </Link>
                  {p.self && <MonitorOnlyTag />}
                </span>
              </td>
              <td className="px-3 py-3">
                <span
                  className="block max-w-[360px] truncate pt-0.5 font-mono text-[12.5px] text-muted"
                  title={p.path}
                >
                  {p.path}
                </span>
                {p.status === 'notfound' && (
                  <span className="mt-1 block text-[13px] text-muted">
                    {p.missingSince === ''
                      ? 'Folder missing since the last scan.'
                      : `Folder missing since the ${formatLastScan(p.missingSince)} scan.`}{' '}
                    Not removed automatically.
                  </span>
                )}
              </td>
              <td className="px-3 py-3">
                <StatusBadge status={p.status} />
              </td>
              <td className="px-3 py-3">
                <Meter
                  label={formatCpu(p.cpuPercent)}
                  fraction={p.cpuPercent === null ? null : p.cpuPercent / 100}
                  color="#5AA7FF"
                  testId="cpu-bar"
                />
              </td>
              <td className="px-3 py-3">
                <Meter
                  label={formatMem(p.memBytes)}
                  fraction={p.memBytes === null ? null : p.memBytes / RAM_RANGE}
                  color="#F0913A"
                  testId="ram-bar"
                />
              </td>
              <td className="px-3 py-3 text-[13px]">
                <span className="flex flex-col">
                  <DatabaseCell databases={p.databases} />
                </span>
              </td>
              <td className="py-2 pr-4 pl-3">
                <div className="flex items-center justify-end gap-2.5">
                  <ProjectSwitch
                    name={p.displayName}
                    status={p.status as Status}
                    actions={p.actions}
                    self={p.self}
                    dockerDown={dockerDown}
                    onStart={() => onStart(p)}
                    onStop={() => onStop(p)}
                  />
                  <ActionMenu
                    name={p.displayName}
                    actions={p.actions}
                    status={p.status}
                    self={p.self}
                    dockerDown={dockerDown}
                    onStart={() => onStart(p)}
                    onRestart={() => onRestart(p)}
                    onUntrack={() => onUntrack(p)}
                  />
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
