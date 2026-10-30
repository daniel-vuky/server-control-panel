import { CloudUpload, Folder, Globe, Play, RotateCw, Square } from 'lucide-react'
import { type KeyboardEvent, type ReactNode, useEffect, useId, useRef } from 'react'
import { Link, Outlet, useLocation, useNavigate, useParams } from 'react-router'
import type { Project } from '../../api/types.gen'
import { useLive } from '../../app/live'
import MonitorOnlyTag from '../../components/MonitorOnlyTag'
import StatusBadge from '../../components/StatusBadge'
import { isNotCreated } from '../../lib/containers'
import { formatUptime } from '../../lib/format'
import { plural } from '../../lib/plural'
import { disabledReason } from '../../lib/status'
import { folderMissing } from '../../lib/time'
import { useNow } from '../../lib/useNow'
import { useProjectActions } from '../../lib/useProjectActions'
import {
  type ProjectActions,
  type ProjectOutlet,
  parseProjectId,
  useProjectFromLive,
} from './projectContext'

/**
 * ProjectPage is /projects/:id: the breadcrumb, the header with the lifecycle
 * buttons, the tabs and the open tab. The project comes from the live state;
 * one action flow (confirm, in-between status, toasts) serves the header and
 * every tab, and its dialogs render here once.
 */
export default function ProjectPage() {
  const params = useParams()
  const id = parseProjectId(params.id)
  const { project, loading } = useProjectFromLive(id)
  const live = useLive()
  const actions = useProjectActions()
  const navigate = useNavigate()

  // A project removed from here has no page any more: back to the list.
  const removed = id !== null && actions.removedIds.has(id)
  useEffect(() => {
    if (removed) navigate('/', { replace: true })
  }, [removed, navigate])

  let body: ReactNode
  if (removed) {
    // On its way back to the list: nothing to show for the paint before it goes.
    body = null
  } else if (loading) {
    body = <Loading />
  } else if (project === null) {
    body = <NotTracked />
  } else {
    const shown = actions.overlay(project)
    const outlet: ProjectOutlet = {
      project: shown,
      raw: project,
      dockerDown: live.dockerUp === false,
      actions,
    }
    body = (
      <>
        <Breadcrumb name={shown.displayName} />
        <Header project={shown} raw={project} dockerDown={outlet.dockerDown} actions={actions} />
        <Tabs base={`/projects/${project.id}`} databases={shown.databases.length}>
          <Outlet context={outlet} />
        </Tabs>
      </>
    )
  }

  return (
    <div className="flex max-w-[1040px] flex-col gap-5">
      {body}
      {actions.dialogs}
    </div>
  )
}

function Loading() {
  return (
    <div role="status" className="flex flex-col gap-5">
      <span className="sr-only">Loading project…</span>
      <div aria-hidden="true" className="h-3.5 w-40 animate-pulse rounded bg-border" />
      <div aria-hidden="true" className="flex flex-col gap-3">
        <div className="h-7 w-1/3 animate-pulse rounded bg-border" />
        <div className="h-3.5 w-1/2 animate-pulse rounded bg-border" />
      </div>
      <div
        aria-hidden="true"
        className="h-[220px] animate-pulse rounded-[10px] border border-border bg-surface"
      />
    </div>
  )
}

function NotTracked() {
  return (
    <div className="flex flex-col items-start gap-3 rounded-xl border border-dashed border-border-strong p-5">
      <p className="m-0 font-semibold">This project is not tracked.</p>
      <Link
        to="/"
        className="flex h-9 items-center rounded-[7px] border border-border-strong px-3.5 text-text no-underline"
      >
        Back to the Dashboard
      </Link>
    </div>
  )
}

/** Breadcrumb goes back to the Dashboard, which restores its own scroll position. */
function Breadcrumb({ name }: { name: string }) {
  return (
    <nav aria-label="Breadcrumb" className="flex min-w-0 items-center gap-2 text-[13px] text-muted">
      <Link to="/" className="text-muted no-underline hover:text-white">
        Projects
      </Link>
      <span aria-hidden="true">/</span>
      <span aria-current="page" className="truncate text-text">
        {name}
      </span>
    </nav>
  )
}

const BUTTON =
  'flex h-9 cursor-pointer items-center gap-[7px] rounded-[7px] border px-3.5 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:border-[#22272e] disabled:bg-transparent disabled:text-[#5e6672]'
const NEUTRAL = `${BUTTON} border-border-strong bg-surface-2 text-text`
const DANGER = `${BUTTON} border-[#6b262c] bg-[#1e0f11] text-[#ff8c8f]`

type HeaderProps = {
  /** project is what the header shows: the live project with any in-between status. */
  project: Project
  /** raw is the project as the server sent it, which the actions take. */
  raw: Project
  dockerDown: boolean
  actions: ProjectActions
}

/** Header is design 02's: name, status, containers, path, domain, uptime and the buttons. */
function Header({ project: p, raw, dockerDown, actions }: HeaderProps) {
  const now = useNow(60_000)
  const containers = p.containers.filter((c) => !isNotCreated(c)).length
  const reason = disabledReason(p.status, p.self, dockerDown)
  const allowed = (action: string) => p.actions.includes(action)

  return (
    <header
      data-testid="project-header"
      className="flex flex-wrap items-start justify-between gap-[18px] pb-1"
    >
      <div className="flex min-w-0 flex-col gap-2.5">
        <div className="flex flex-wrap items-center gap-3">
          <h1 className="m-0 font-mono text-[26px] font-semibold tracking-[-0.02em] break-all">
            {p.displayName}
          </h1>
          <StatusBadge status={p.status} />
          {p.self && <MonitorOnlyTag />}
          <span className="flex h-[22px] items-center gap-1.5 rounded-[5px] border border-border-strong px-2 text-xs whitespace-nowrap text-text-2">
            Docker{' '}
            <span className="text-muted">· {plural(containers, 'container', 'containers')}</span>
          </span>
        </div>
        {p.status === 'notfound' && (
          <p className="m-0 text-[13px] text-muted">{folderMissing(p.missingSince)}</p>
        )}
        <div className="flex flex-wrap items-center gap-x-[18px] gap-y-2 text-[13px] text-muted">
          <span className="flex min-w-0 items-center gap-1.5">
            <Folder aria-hidden="true" size={14} strokeWidth={1.8} className="flex-none" />
            <span className="font-mono break-all text-text-2">{p.path}</span>
          </span>
          {p.publicDomain !== '' && (
            <a
              href={`https://${p.publicDomain}`}
              target="_blank"
              rel="noopener noreferrer"
              className="flex items-center gap-1.5 text-text-2 no-underline hover:text-white"
            >
              <Globe aria-hidden="true" size={14} strokeWidth={1.8} className="flex-none" />
              {p.publicDomain}
            </a>
          )}
          <span>
            Uptime <span className="font-mono text-text-2">{formatUptime(p.upSince, now)}</span>
          </span>
        </div>
      </div>

      <div className="flex flex-wrap gap-2">
        <button
          type="button"
          className={NEUTRAL}
          disabled={!allowed('start')}
          title={allowed('start') ? undefined : reason}
          onClick={() => actions.start(raw)}
        >
          <Play aria-hidden="true" size={14} fill="currentColor" strokeWidth={0} />
          Start
        </button>
        <button
          type="button"
          className={DANGER}
          disabled={!allowed('stop')}
          title={allowed('stop') ? undefined : reason}
          onClick={() => actions.stop(raw)}
        >
          <Square aria-hidden="true" size={13} fill="currentColor" strokeWidth={0} />
          Stop
        </button>
        <button
          type="button"
          className={NEUTRAL}
          disabled={!allowed('restart')}
          title={allowed('restart') ? undefined : reason}
          onClick={() => actions.restart(raw)}
        >
          <RotateCw aria-hidden="true" size={15} strokeWidth={1.9} />
          Restart
        </button>
        <button
          type="button"
          disabled
          title="Backups are not set up yet"
          className={`${BUTTON} border-transparent bg-text px-4 font-semibold text-bg`}
        >
          <CloudUpload aria-hidden="true" size={15} strokeWidth={2} />
          Back up now
        </button>
      </div>
    </header>
  )
}

const TABS = [
  { key: '', label: 'Overview' },
  { key: 'logs', label: 'Logs' },
  { key: 'database', label: 'Database & Backup' },
  { key: 'settings', label: 'Settings' },
] as const

/**
 * Tabs are design 02's tab row as links, one URL each, with the open tab's
 * panel below. Arrow keys, Home and End move between them; only the open tab
 * is in the Tab order.
 */
function Tabs({
  base,
  databases,
  children,
}: {
  base: string
  databases: number
  children: ReactNode
}) {
  const { pathname } = useLocation()
  const prefix = useId()
  const refs = useRef<Array<HTMLAnchorElement | null>>([])
  const rest = pathname.slice(base.length).replace(/^\/+|\/+$/g, '')
  const open = Math.max(
    0,
    TABS.findIndex((t) => t.key === rest),
  )

  const onKeyDown = (e: KeyboardEvent<HTMLAnchorElement>, i: number) => {
    const n = TABS.length
    const next =
      e.key === 'ArrowRight'
        ? (i + 1) % n
        : e.key === 'ArrowLeft'
          ? (i - 1 + n) % n
          : e.key === 'Home'
            ? 0
            : e.key === 'End'
              ? n - 1
              : -1
    if (next < 0) return
    e.preventDefault()
    refs.current[next]?.focus()
  }

  return (
    <>
      <div
        role="tablist"
        aria-label="Project detail sections"
        className="-mt-1 flex gap-1 overflow-x-auto border-b border-border"
      >
        {TABS.map((t, i) => {
          const selected = i === open
          return (
            <Link
              key={t.key}
              ref={(el) => {
                refs.current[i] = el
              }}
              id={`${prefix}-tab-${i}`}
              role="tab"
              aria-selected={selected}
              aria-controls={`${prefix}-panel`}
              tabIndex={selected ? 0 : -1}
              to={t.key === '' ? base : `${base}/${t.key}`}
              onKeyDown={(e) => onKeyDown(e, i)}
              className={`flex h-[42px] flex-none items-center gap-2 px-3.5 text-sm whitespace-nowrap no-underline focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent ${
                selected
                  ? 'font-semibold text-white shadow-[inset_0_-2px_0_#e7e9ec]'
                  : 'text-muted hover:text-text'
              }`}
            >
              {t.label}
              {t.key === 'database' && databases > 0 && (
                <>
                  <span
                    aria-hidden="true"
                    className="rounded-full bg-border px-1.5 font-mono text-[11px] font-normal text-text-2"
                  >
                    {databases}
                  </span>
                  <span className="sr-only">, {plural(databases, 'database', 'databases')}</span>
                </>
              )}
            </Link>
          )
        })}
      </div>
      <div id={`${prefix}-panel`} role="tabpanel" aria-label={TABS[open].label}>
        {children}
      </div>
    </>
  )
}
