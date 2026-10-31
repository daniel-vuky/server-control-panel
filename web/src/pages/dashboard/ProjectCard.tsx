import { ExternalLink } from 'lucide-react'
import { useId } from 'react'
import { Link } from 'react-router'
import type { Project } from '../../api/types.gen'
import ActionMenu from '../../components/ActionMenu'
import MonitorOnlyTag from '../../components/MonitorOnlyTag'
import ProjectSwitch from '../../components/ProjectSwitch'
import StatusBadge from '../../components/StatusBadge'
import { formatCpu, formatMem, formatUptime } from '../../lib/format'
import type { Status } from '../../lib/status'
import { folderMissing } from '../../lib/time'
import { issueOf } from './issue'
import type { LastBackup } from './lastBackup'
import Meter, { RAM_RANGE } from './Meter'
import { stackNote } from './stack'

/** ProjectItem is one project as a card or a table row shows it, with its controls wired. */
export type ProjectItem = {
  project: Project
  /** latest is the Last backup line; null when the read failed. */
  latest: LastBackup | null
  dockerDown: boolean
  onStart(): void
  onStop(): void
  onRestart(): void
  onUntrack(): void
  /** backup is the Back up now menu item. */
  backup: { enabled: boolean; reason: string; onRun(): void }
}

const FOCUS = 'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent'

/** CPU_COLOR and RAM_COLOR are design 01's bar colours. */
export const CPU_COLOR = '#5AA7FF'
export const RAM_COLOR = '#F0913A'

export const cpuFraction = (p: Project) => (p.cpuPercent === null ? null : p.cpuPercent / 100)
export const ramFraction = (p: Project) => (p.memBytes === null ? null : p.memBytes / RAM_RANGE)

/**
 * uptimeOrIssue is the third value of a project (D17): an Error project's
 * issue, in red, else how long it has been up ("—" when not known).
 */
export function uptimeOrIssue(p: Project): { label: 'Uptime' | 'Issue'; text: string } {
  return p.status === 'error'
    ? { label: 'Issue', text: issueOf(p) }
    : { label: 'Uptime', text: formatUptime(p.upSince) }
}

/** ItemMenu is the ⋯ menu of a card or row: Start, Restart, Back up now, View logs, Open details and the red item. */
export function ItemMenu({ item }: { item: ProjectItem }) {
  const p = item.project
  const missing = p.status === 'notfound'
  return (
    <ActionMenu
      name={p.displayName}
      actions={p.actions}
      status={p.status}
      self={p.self}
      dockerDown={item.dockerDown}
      onStart={item.onStart}
      onRestart={item.onRestart}
      onUntrack={item.onUntrack}
      backup={item.backup}
      logsHref={missing ? undefined : `/projects/${p.id}/logs`}
      logsDisabledReason={missing ? 'The project folder is missing' : undefined}
      detailsHref={`/projects/${p.id}`}
    />
  )
}

/** ItemSwitch is the Start/Stop switch with its On/Off word. */
export function ItemSwitch({ item }: { item: ProjectItem }) {
  const p = item.project
  return (
    <ProjectSwitch
      name={p.displayName}
      status={p.status as Status}
      actions={p.actions}
      self={p.self}
      dockerDown={item.dockerDown}
      onStart={item.onStart}
      onStop={item.onStop}
    />
  )
}

/**
 * Domain is the project's public domain as a link in a new tab (D15), muted
 * "No public domain" without one, or the folder-missing words for a Not found
 * project.
 */
export function Domain({ project: p }: { project: Project }) {
  if (p.status === 'notfound') {
    return (
      <span className="text-[13px] leading-snug text-muted">{folderMissing(p.missingSince)}</span>
    )
  }
  if (p.publicDomain === '') {
    return <span className="text-[13px] text-muted">No public domain</span>
  }
  return (
    <a
      href={`https://${p.publicDomain}`}
      target="_blank"
      rel="noopener noreferrer"
      className={`flex w-fit max-w-full min-w-0 items-center gap-[5px] text-[13px] text-muted no-underline hover:text-text ${FOCUS}`}
    >
      <span className="truncate">{p.publicDomain}</span>
      <ExternalLink aria-hidden="true" size={12} strokeWidth={2} className="flex-none" />
    </a>
  )
}

const DOT: Record<LastBackup['tone'], string> = {
  ok: 'bg-success',
  bad: 'bg-danger',
  none: 'bg-[#4a515c]',
}

/** LastBackupLine is the dot and words of D18, or "—" when the read failed. */
export function LastBackupLine({ latest }: { latest: LastBackup | null }) {
  return (
    <span data-testid="last-backup" className="flex min-w-0 items-center gap-1.5 text-[13px]">
      {latest === null ? (
        '—'
      ) : (
        <>
          <span
            data-testid="backup-dot"
            aria-hidden="true"
            className={`size-[7px] flex-none rounded-full ${DOT[latest.tone]}`}
          />
          <span className="truncate">{latest.text}</span>
        </>
      )}
    </span>
  )
}

/**
 * ProjectCard is one project of the Dashboard's card view (design 01, D13):
 * status, stack chip and ⋯ menu; the name and domain; CPU, RAM and the uptime
 * (or an Error card's issue); then the last backup and the switch. An Error
 * card has the red edge.
 */
export default function ProjectCard(item: ProjectItem) {
  const p = item.project
  const nameId = useId()
  const error = p.status === 'error'
  const [stack, ...note] = stackNote(p).split(' · ')
  const third = uptimeOrIssue(p)

  return (
    <article
      aria-labelledby={nameId}
      className={`box-border flex min-w-0 flex-col gap-3.5 rounded-xl border bg-surface pt-4 pr-4 pb-3.5 pl-[18px] ${
        error
          ? 'border-error-border shadow-[inset_0_0_0_1px_rgba(242,85,90,0.08)]'
          : 'border-border'
      }`}
    >
      <div className="flex items-center gap-2">
        <StatusBadge status={p.status} />
        <span
          data-testid="stack-chip"
          className="flex h-[22px] items-center gap-1.5 rounded-[5px] border border-border-strong px-2 text-xs whitespace-nowrap text-text-2"
        >
          {stack}
          {note.length > 0 && (
            <>
              {' '}
              <span className="text-muted">· {note.join(' · ')}</span>
            </>
          )}
        </span>
        <div className="ml-auto">
          <ItemMenu item={item} />
        </div>
      </div>

      <div className="flex min-w-0 flex-col gap-1">
        <div className="flex min-w-0 items-center gap-2">
          <Link
            id={nameId}
            to={`/projects/${p.id}`}
            className={`truncate font-mono text-lg font-semibold tracking-[-0.01em] text-white no-underline hover:underline ${FOCUS}`}
          >
            {p.displayName}
          </Link>
          {p.self && <MonitorOnlyTag />}
        </div>
        <Domain project={p} />
      </div>

      <div
        data-testid="card-strip"
        className="grid grid-cols-3 gap-2.5 border-y border-border py-3"
      >
        <Stat label="CPU">
          <Meter
            label={formatCpu(p.cpuPercent)}
            fraction={cpuFraction(p)}
            color={CPU_COLOR}
            testId="cpu-bar"
            className="text-[15px]"
          />
        </Stat>
        <Stat label="RAM">
          <Meter
            label={formatMem(p.memBytes)}
            fraction={ramFraction(p)}
            color={RAM_COLOR}
            testId="ram-bar"
            className="text-[15px]"
          />
        </Stat>
        <Stat label={third.label}>
          {error ? (
            <span className="text-[13px] leading-[1.3] break-words text-danger-text">
              {third.text}
            </span>
          ) : (
            <span className="font-mono text-[15px] tabular-nums">{third.text}</span>
          )}
        </Stat>
      </div>

      <div className="flex items-center gap-2.5">
        <div className="flex min-w-0 flex-col gap-[3px]">
          <span className="text-[11.5px] text-muted">Last backup</span>
          <LastBackupLine latest={item.latest} />
        </div>
        <div className="ml-auto">
          <ItemSwitch item={item} />
        </div>
      </div>
    </article>
  )
}

/** Stat is one column of the card's strip: a small label over its value. */
function Stat({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-1.5">
      <span className="text-[11.5px] text-muted">{label}</span>
      {children}
    </div>
  )
}
