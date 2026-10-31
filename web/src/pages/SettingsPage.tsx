import { type ReactNode, useRef, useState } from 'react'
import { Link } from 'react-router'
import type { Workspace } from '../api/types.gen'
import { listWorkspaces, removeWorkspace } from '../api/workspaces'
import ConfirmDialog from '../components/ConfirmDialog'
import IssueLinks from '../components/IssueLinks'
import LoadError from '../components/LoadError'
import { useToast } from '../components/Toast'
import { plural } from '../lib/plural'
import { formatLastScan } from '../lib/time'
import { useLoad } from '../lib/useLoad'
import { useRescan } from '../lib/useRescan'
import BackupDefaultsCard from './settings/BackupDefaultsCard'
import DriveCard from './settings/DriveCard'
import MetricsHistoryCard from './settings/MetricsHistoryCard'

const card = 'rounded-[10px] border border-border bg-surface'
const rowButton =
  'h-8 cursor-pointer rounded-md border bg-transparent px-3 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-60'

const mono = (text: string): ReactNode => <span className="font-mono">{text}</span>

const head = 'flex flex-col gap-1 border-b border-border px-[18px] py-4'

/** SettingsPage is /settings: workspaces, Google Drive, alerts (still to come), backup defaults and metrics history. */
export default function SettingsPage() {
  return (
    <div className="flex max-w-[1040px] flex-col gap-[18px]">
      <header className="flex flex-col gap-1.5">
        <h1 className="text-2xl font-semibold tracking-tight">Settings</h1>
        <p className="text-[13px] text-muted">
          Workspaces, Google Drive, alert channels and backup defaults
        </p>
      </header>

      <WorkspacesCard />

      <DriveCard />
      <Soon id="alert-title" title="Alerts" issues={[8]}>
        Where the panel sends alerts.
      </Soon>
      <BackupDefaultsCard />
      <MetricsHistoryCard />
    </div>
  )
}

function WorkspacesCard() {
  const { load, retry } = useLoad(listWorkspaces)
  const toast = useToast()
  const [confirm, setConfirm] = useState<Workspace | null>(null)
  // Ids whose removal is out, so a second confirm cannot post it again.
  const removing = useRef(new Set<number>())

  const remove = async (w: Workspace) => {
    if (removing.current.has(w.id)) return
    removing.current.add(w.id)
    try {
      await removeWorkspace(w.id)
    } catch (err) {
      const message = err instanceof Error && err.message ? err.message : 'Something went wrong.'
      toast.error(<>Could not remove {mono(w.path)}</>, message)
      return
    } finally {
      removing.current.delete(w.id)
    }
    retry()
    toast.success(<>Removed {mono(w.path)}</>)
  }

  return (
    <section aria-labelledby="ws-title" className={card}>
      <div className="flex flex-wrap items-center justify-between gap-2.5 border-b border-border px-[18px] py-4">
        <div className="flex flex-col gap-1">
          <h2 id="ws-title" className="text-[15px] font-semibold">
            Workspaces
          </h2>
          <span className="text-[12.5px] text-muted">
            Only scans inside these folders. Blocks <span className="font-mono">..</span> and
            symlinks pointing outside.
          </span>
        </div>
        <Link
          to="/onboarding"
          className="flex h-[34px] items-center rounded-[7px] border border-border-strong px-3.5 text-text"
        >
          Add workspace
        </Link>
      </div>

      {load.state === 'loading' && (
        <p role="status" className="px-[18px] py-3.5 text-[13px] text-muted">
          Loading workspaces…
        </p>
      )}
      {load.state === 'error' && (
        <div className="p-3.5">
          <LoadError title="Could not load workspaces." message={load.message} onRetry={retry} />
        </div>
      )}
      {load.state === 'ok' &&
        (load.data.length === 0 ? (
          <p className="px-[18px] py-3.5 text-[13px] text-muted">No workspace yet</p>
        ) : (
          <ul>
            {load.data.map((w) => (
              <WorkspaceRow key={w.id} workspace={w} onRemove={() => setConfirm(w)} />
            ))}
          </ul>
        ))}

      {confirm && (
        <ConfirmDialog
          title={<>Remove workspace {mono(confirm.path)}?</>}
          body={`Its ${confirm.tracked} tracked ${confirm.tracked === 1 ? 'project leaves' : 'projects leave'} the panel. Nothing on the server is stopped, deleted or changed, and backups on Google Drive stay where they are.`}
          confirmLabel="Remove"
          tone="danger"
          onCancel={() => setConfirm(null)}
          onConfirm={() => {
            setConfirm(null)
            void remove(confirm)
          }}
        />
      )}
    </section>
  )
}

function WorkspaceRow({ workspace: w, onRemove }: { workspace: Workspace; onRemove(): void }) {
  const { starting, rescan } = useRescan()

  return (
    <li className="flex flex-wrap items-center gap-x-5 gap-y-1 border-b border-border px-[18px] py-3.5 last:border-b-0">
      <span className="font-mono font-semibold break-all">{w.path}</span>
      <span className="text-[13px] text-muted">
        {`Depth ${w.depth} · ${plural(w.tracked, 'project', 'projects')} tracked · ${w.ignored} ignored · last scanned ${formatLastScan(w.lastScanAt)}`}
      </span>
      <div className="ml-auto flex gap-2">
        <button
          type="button"
          disabled={starting}
          aria-label={`Rescan ${w.path}`}
          onClick={() => void rescan(w.id)}
          className={`${rowButton} border-border-strong text-text`}
        >
          Rescan
        </button>
        <button
          type="button"
          aria-label={`Remove ${w.path}`}
          onClick={onRemove}
          className={`${rowButton} border-[#6B262C] text-[#FF8C8F]`}
        >
          Remove
        </button>
      </div>
    </li>
  )
}

function Soon({
  id,
  title,
  issues,
  children,
}: {
  id: string
  title: string
  issues: number[]
  children: ReactNode
}) {
  return (
    <section aria-labelledby={id} className={card}>
      <div className={head}>
        <h2 id={id} className="text-[15px] font-semibold">
          {title}
        </h2>
        <span className="text-[12.5px] text-muted">{children}</span>
      </div>
      <p className="px-[18px] py-3.5 text-[13px] text-muted">
        Coming in <IssueLinks issues={issues} />
      </p>
    </section>
  )
}
