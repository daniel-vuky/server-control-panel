import { FolderPlus } from 'lucide-react'
import { type ReactNode, useCallback, useEffect, useRef, useState } from 'react'
import { Link } from 'react-router'
import { type LifecycleAction, listProjects, runAction } from '../api/projects'
import type { Project } from '../api/types.gen'
import { useLive } from '../app/live'
import ActionMenu from '../components/ActionMenu'
import ConfirmDialog from '../components/ConfirmDialog'
import DatabaseCell from '../components/DatabaseCell'
import LoadError from '../components/LoadError'
import MonitorOnlyTag from '../components/MonitorOnlyTag'
import ProjectSwitch from '../components/ProjectSwitch'
import StatusBadge from '../components/StatusBadge'
import { useToast } from '../components/Toast'
import { inFlightStatus, type Status } from '../lib/status'
import { useLoad } from '../lib/useLoad'

/** PENDING_MS is how long a finished command's status outlives it when no state event arrives. */
const PENDING_MS = 5000

const VERB: Record<LifecycleAction, { done: string; failed: string }> = {
  start: { done: 'Started', failed: "Couldn't start" },
  stop: { done: 'Stopped', failed: "Couldn't stop" },
  restart: { done: 'Restarted', failed: "Couldn't restart" },
}

const mono = (name: string): ReactNode => <span className="font-mono">{name}</span>

/** Pending is what a row shows while a command runs, and until the next state or PENDING_MS after. */
type Pending = { status: Status; actions: string[] }

type Confirm = { action: 'stop' | 'restart'; id: number; name: string }

/**
 * useProjectActions runs lifecycle commands and keeps the optimistic status of
 * each project until the next live state or five seconds, whichever is first.
 */
function useProjectActions() {
  const toast = useToast()
  const live = useLive()
  const [pending, setPending] = useState<Record<number, Pending>>({})
  const inflight = useRef(new Set<number>())
  const timers = useRef(new Map<number, ReturnType<typeof setTimeout>>())
  const mounted = useRef(true)

  useEffect(() => {
    mounted.current = true
    const held = timers.current
    return () => {
      mounted.current = false
      for (const t of held.values()) clearTimeout(t)
      held.clear()
    }
  }, [])

  const forget = useCallback((id: number) => {
    const t = timers.current.get(id)
    if (t !== undefined) clearTimeout(t)
    timers.current.delete(id)
  }, [])

  // A new state event is the truth again. Only a settled row holds a timer,
  // so a command still running keeps its in-flight status.
  // biome-ignore lint/correctness/useExhaustiveDependencies: only a new state event re-runs this.
  useEffect(() => {
    if (live.projects === null) return
    const settled = [...timers.current.keys()]
    if (settled.length === 0) return
    for (const id of settled) forget(id)
    setPending((prev) => {
      if (!settled.some((id) => id in prev)) return prev
      const next = { ...prev }
      for (const id of settled) delete next[id]
      return next
    })
  }, [live.projects])

  const run = useCallback(
    async (id: number, name: string, action: LifecycleAction) => {
      const verb = VERB[action]
      forget(id)
      inflight.current.add(id)
      // While a command runs nothing else may start, so the row has no actions.
      setPending((p) => ({ ...p, [id]: { status: inFlightStatus(action), actions: [] } }))

      let settled: Pending | null = null
      try {
        const result = await runAction(id, action)
        settled = { status: result.status as Status, actions: result.actions }
        if (result.outcome === 'ok') {
          toast.success(
            <>
              {verb.done} {mono(name)}
            </>,
          )
        } else {
          toast.error(
            <>
              {verb.failed} {mono(name)}
            </>,
            result.message,
          )
        }
      } catch (err) {
        const message = err instanceof Error && err.message ? err.message : 'Something went wrong.'
        toast.error(
          <>
            {verb.failed} {mono(name)}
          </>,
          message,
        )
      } finally {
        inflight.current.delete(id)
      }

      // After the page is gone there is no row to settle and no timer to start.
      if (!mounted.current) return
      if (settled === null) {
        setPending(({ [id]: _gone, ...rest }) => rest)
        return
      }
      const done = settled
      setPending((p) => ({ ...p, [id]: done }))
      timers.current.set(
        id,
        setTimeout(() => {
          timers.current.delete(id)
          if (!mounted.current || inflight.current.has(id)) return
          setPending(({ [id]: _gone, ...rest }) => rest)
        }, PENDING_MS),
      )
    },
    [forget, toast],
  )

  return { pending, run }
}

/** DashboardPage is /: the tracked projects as a table (cards come with E3-F08). */
export default function DashboardPage() {
  const { load, retry } = useLoad(listProjects)
  const live = useLive()
  const { pending, run } = useProjectActions()
  const [confirm, setConfirm] = useState<Confirm | null>(null)

  const dockerDown = live.dockerUp === false
  const base = live.projects ?? (load.state === 'ok' ? load.data : null)
  const rows = base?.map((p) => overlay(p, pending[p.id]))

  const stop = (p: Project) => setConfirm({ action: 'stop', id: p.id, name: p.displayName })
  const restart = (p: Project) => {
    // Only a project that is up asks first; from error it just starts again.
    if (p.status === 'running' || p.status === 'partial') {
      setConfirm({ action: 'restart', id: p.id, name: p.displayName })
    } else {
      void run(p.id, p.displayName, 'restart')
    }
  }
  const start = (p: Project) => void run(p.id, p.displayName, 'start')

  return (
    <section className="flex max-w-[1040px] flex-col gap-[18px]">
      <header className="flex flex-col gap-1.5">
        <h1 className="text-2xl font-semibold tracking-tight">Dashboard</h1>
        <p className="text-[13px] text-muted">Tracked projects and their status</p>
      </header>

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
            onStart={start}
            onStop={stop}
            onRestart={restart}
          />
        ))}

      {confirm && (
        <ConfirmDialog
          title={
            <>
              {confirm.action === 'stop' ? 'Stop' : 'Restart'} {mono(confirm.name)}?
            </>
          }
          body={
            confirm.action === 'stop'
              ? 'It will stop serving until you start it again. Its volumes and data are kept.'
              : 'Its containers will stop and start again; it is unavailable for a moment. Volumes and data are kept.'
          }
          command={`docker compose ${confirm.action}`}
          confirmLabel={confirm.action === 'stop' ? 'Stop' : 'Restart'}
          tone={confirm.action === 'stop' ? 'danger' : 'primary'}
          onCancel={() => setConfirm(null)}
          onConfirm={() => {
            setConfirm(null)
            void run(confirm.id, confirm.name, confirm.action)
          }}
        />
      )}
    </section>
  )
}

/** overlay shows the status a command set optimistically, with the actions the server sent for it. */
function overlay(p: Project, pending: Pending | undefined): Project {
  return pending === undefined ? p : { ...p, status: pending.status, actions: pending.actions }
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
}

function ProjectTable({ projects, dockerDown, onStart, onStop, onRestart }: TableProps) {
  return (
    <div className="overflow-x-auto rounded-[10px] border border-border bg-surface">
      <table className="w-full min-w-[900px] border-collapse text-left">
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
                  <span className="font-mono font-semibold">{p.displayName}</span>
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
              </td>
              <td className="px-3 py-3">
                <StatusBadge status={p.status} />
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
