import { FolderPlus, RotateCw, SearchX } from 'lucide-react'
import { useEffect, useLayoutEffect, useMemo, useRef } from 'react'
import { useNavigate, useSearchParams } from 'react-router'
import { listProjects } from '../api/projects'
import type { Project, Workspace } from '../api/types.gen'
import { listWorkspaces } from '../api/workspaces'
import { useLatestBackups } from '../app/latestBackups'
import { LiveDim, useLive } from '../app/live'
import EmptyState, { EmptyStateButton, EmptyStateLink } from '../components/EmptyState'
import LoadError from '../components/LoadError'
import PageHeader from '../components/PageHeader'
import Skeleton from '../components/Skeleton'
import { projectRule } from '../lib/backup'
import { formatLastScan } from '../lib/time'
import { useBackup } from '../lib/useBackup'
import { useLoad } from '../lib/useLoad'
import { useProjectActions } from '../lib/useProjectActions'
import { useRescan } from '../lib/useRescan'
import {
  countByStatus,
  type Filters,
  filtersOf,
  matches,
  readStoredView,
  storeView,
  type View,
} from './dashboard/filters'
import { lastBackupOf, latestByProject } from './dashboard/lastBackup'
import type { ProjectItem } from './dashboard/ProjectCard'
import ProjectCards from './dashboard/ProjectCards'
import ProjectFilters from './dashboard/ProjectFilters'
import ProjectRows from './dashboard/ProjectRows'
import QuickCounts from './dashboard/QuickCounts'
import ServerSummary from './dashboard/ServerSummary'

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

/**
 * DashboardPage is / (design 01): the server summary, the quick counts and
 * the tracked projects as cards or a table, filtered by the URL query
 * (`status`, `q`, `view`; with no `view`, the remembered one).
 */
export default function DashboardPage() {
  const { load, retry } = useLoad(listProjects)
  const live = useLive()
  const actions = useProjectActions()
  const backup = useBackup()
  const latest = useLatestBackups()
  const navigate = useNavigate()
  const workspaces = useLoad(listWorkspaces).load
  const [params, setParams] = useSearchParams()
  const filters = filtersOf(params, params.has('view') ? null : readStoredView())

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

  const byProject = useMemo(() => latestByProject(latest.data), [latest.data])
  const driveState = live.drive?.state

  /**
   * change writes the filters to the URL: All and an empty search leave the
   * query, typing replaces the entry, and a chosen view is remembered.
   */
  const change = (next: Partial<Filters>) => {
    if (next.view !== undefined) storeView(next.view)
    const typing = next.q !== undefined && next.status === undefined && next.view === undefined
    setParams(
      (prev) => {
        const p = new URLSearchParams(prev)
        if (next.status !== undefined) {
          if (next.status === 'all') p.delete('status')
          else p.set('status', next.status)
        }
        if (next.q !== undefined) {
          if (next.q === '') p.delete('q')
          else p.set('q', next.q)
        }
        if (next.view !== undefined) p.set('view', next.view)
        return p
      },
      { replace: typing },
    )
  }

  const clear = () =>
    setParams((prev) => {
      const p = new URLSearchParams(prev)
      p.delete('status')
      p.delete('q')
      return p
    })

  const item = (p: Project): ProjectItem => {
    const rule = projectRule(p, driveState)
    const pending = backup.pendingProjectId === p.id
    return {
      project: p,
      latest: lastBackupOf(byProject, p.id),
      dockerDown,
      onStart: () => actions.start(p),
      onStop: () => actions.stop(p),
      onRestart: () => actions.restart(p),
      onUntrack: () => actions.leave(p),
      backup: {
        enabled: rule.enabled && !pending,
        reason: pending ? 'A backup is starting' : rule.reason,
        onRun: () => backup.start(p),
      },
    }
  }

  const shown = rows?.filter((p) => matches(p, filters))
  const counts = rows === undefined ? null : countByStatus(rows)

  return (
    <section className="flex flex-col gap-[18px]">
      <Header workspaces={workspaces.state === 'ok' ? workspaces.data : null} />

      <LiveDim className="flex flex-col gap-[18px]">
        <ServerSummary />
        {rows === undefined && load.state === 'loading' && <Loading view={filters.view} />}
        {rows === undefined && load.state === 'error' && (
          <LoadError title="Could not load projects." message={load.message} onRetry={retry} />
        )}
        {rows !== undefined && counts !== null && (
          <>
            <QuickCounts counts={counts} latest={latest.data ? latest.data.latest : undefined} />
            {rows.length === 0 ? (
              <Empty />
            ) : (
              <>
                <ProjectFilters filters={filters} counts={counts} onChange={change} />
                {shown === undefined || shown.length === 0 ? (
                  <NothingMatches onClear={clear} />
                ) : filters.view === 'table' ? (
                  <ProjectRows items={shown.map(item)} />
                ) : (
                  <ProjectCards items={shown.map(item)} />
                )}
              </>
            )}
          </>
        )}
      </LiveDim>

      {actions.dialogs}
      {backup.dialogs}
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
 * Header is the page header: the title, the workspace line of design 01 and the Rescan button.
 * Without the workspaces (loading, failed or none) it keeps the plain subtitle
 * and no button.
 */
function Header({ workspaces }: { workspaces: Workspace[] | null }) {
  const { starting, rescan } = useRescan()
  const known = workspaces !== null && workspaces.length > 0

  return (
    <PageHeader
      title="Dashboard"
      subtitle={
        known ? (
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
        )
      }
      actions={
        known && (
          <button
            type="button"
            disabled={starting}
            onClick={() => void rescan()}
            className="flex h-9 cursor-pointer items-center gap-2 rounded-[7px] border border-border-strong bg-surface-2 px-3.5 text-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-60"
          >
            <RotateCw aria-hidden="true" size={16} strokeWidth={1.8} />
            Rescan
          </button>
        )
      }
    />
  )
}

/**
 * Loading holds the projects' place (D26): the bar, then six card blocks or
 * three row blocks. The summary tiles have their own skeletons.
 */
function Loading({ view }: { view: View }) {
  return (
    <div role="status" className="flex flex-col gap-[18px]">
      <span className="sr-only">Loading projects…</span>
      <div
        data-skeleton-bar=""
        aria-hidden="true"
        className="h-9 w-3/5 animate-scp-pulse rounded-lg bg-surface-2"
      />
      {view === 'table' ? (
        <div className="flex flex-col gap-2">
          {[0, 1, 2].map((i) => (
            <div
              key={i}
              data-skeleton-row=""
              aria-hidden="true"
              className="h-12 animate-scp-pulse rounded-lg border border-border bg-surface"
            />
          ))}
        </div>
      ) : (
        <div className="grid grid-cols-[repeat(auto-fit,minmax(min(300px,100%),1fr))] gap-3.5">
          {[0, 1, 2, 3, 4, 5].map((i) => (
            <div
              key={i}
              data-skeleton-card=""
              aria-hidden="true"
              className="box-border flex h-[210px] flex-col gap-3.5 rounded-xl border border-border bg-surface p-[18px]"
            >
              <Skeleton className="h-3.5 w-2/5" />
              <Skeleton className="h-[22px] w-[65%]" />
              <Skeleton className="h-3 w-1/2" />
              <div className="mt-auto h-8 animate-scp-pulse rounded-md bg-nav-active" />
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

/** Empty is zero tracked projects: design 10's empty state with a way to add a workspace. */
function Empty() {
  return (
    <EmptyState
      icon={FolderPlus}
      title="No projects yet"
      action={<EmptyStateLink to="/onboarding">Add workspace</EmptyStateLink>}
    >
      Add a workspace and the panel will find the compose projects in it.
    </EmptyState>
  )
}

/** NothingMatches is D25: projects exist but none passes the filters. */
function NothingMatches({ onClear }: { onClear(): void }) {
  return (
    <EmptyState
      icon={SearchX}
      title="No projects match these filters."
      action={<EmptyStateButton onClick={onClear}>Clear filters</EmptyStateButton>}
    />
  )
}
