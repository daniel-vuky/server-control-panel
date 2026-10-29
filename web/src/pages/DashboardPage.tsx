import { FolderPlus } from 'lucide-react'
import { Link } from 'react-router'
import { listProjects } from '../api/projects'
import type { Project } from '../api/types.gen'
import DatabaseCell from '../components/DatabaseCell'
import LoadError from '../components/LoadError'
import MonitorOnlyTag from '../components/MonitorOnlyTag'
import StatusBadge from '../components/StatusBadge'
import { useLoad } from '../lib/useLoad'

/** DashboardPage is /: the tracked projects as a table (cards come with E3-F08). */
export default function DashboardPage() {
  const { load, retry } = useLoad(listProjects)

  return (
    <section className="flex max-w-[1040px] flex-col gap-[18px]">
      <header className="flex flex-col gap-1.5">
        <h1 className="text-2xl font-semibold tracking-tight">Dashboard</h1>
        <p className="text-[13px] text-muted">Tracked projects and their status</p>
      </header>

      {load.state === 'loading' && <Loading />}
      {load.state === 'error' && (
        <LoadError title="Could not load projects." message={load.message} onRetry={retry} />
      )}
      {load.state === 'ok' &&
        (load.data.length === 0 ? <Empty /> : <ProjectTable projects={load.data} />)}
    </section>
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

function ProjectTable({ projects }: { projects: Project[] }) {
  return (
    <div className="overflow-x-auto rounded-[10px] border border-border bg-surface">
      <table className="w-full min-w-[720px] border-collapse text-left">
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
            <th scope="col" className="py-2.5 pr-4 pl-3 font-normal">
              Database
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
              <td className="py-3 pr-4 pl-3 text-[13px]">
                <span className="flex flex-col">
                  <DatabaseCell databases={p.databases} />
                </span>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
