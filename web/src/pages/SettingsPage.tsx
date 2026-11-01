import { FolderPlus, Plus } from 'lucide-react'
import { type ReactNode, useRef, useState } from 'react'
import { Link } from 'react-router'
import type { Workspace } from '../api/types.gen'
import { listWorkspaces, removeWorkspace } from '../api/workspaces'
import { LiveDim } from '../app/live'
import ConfirmDialog from '../components/ConfirmDialog'
import EmptyState, { EmptyStateLink } from '../components/EmptyState'
import PageHeader from '../components/PageHeader'
import { useToast } from '../components/Toast'
import { plural } from '../lib/plural'
import { formatLastScan } from '../lib/time'
import { useRead } from '../lib/useRead'
import { useRescan } from '../lib/useRescan'
import AlertsCard from './settings/AlertsCard'
import BackupDefaultsCard from './settings/BackupDefaultsCard'
import CardRead from './settings/CardRead'
import DriveCard from './settings/DriveCard'
import MetricsHistoryCard from './settings/MetricsHistoryCard'

const card = 'rounded-[10px] border border-border bg-surface'
const rowButton =
  'h-8 cursor-pointer rounded-md border bg-transparent px-3 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-60'

const mono = (text: string): ReactNode => <span className="font-mono">{text}</span>

/** SettingsPage is /settings: workspaces, Google Drive, alerts (channels and thresholds), backup defaults and metrics history. */
export default function SettingsPage() {
  return (
    <div className="flex flex-col gap-[18px]">
      <PageHeader
        title="Settings"
        subtitle="Workspaces, Google Drive, alert channels and backup defaults"
      />

      <LiveDim className="flex flex-col gap-[18px]">
        <WorkspacesCard />

        <DriveCard />
        <AlertsCard />
        <BackupDefaultsCard />
        <MetricsHistoryCard />
      </LiveDim>
    </div>
  )
}

function WorkspacesCard() {
  const read = useRead(listWorkspaces, 'workspaces')
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
    read.refresh()
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
          className="flex h-[34px] items-center gap-[7px] rounded-[7px] border border-border-strong bg-surface-2 px-3.5 text-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          <Plus aria-hidden="true" size={14} strokeWidth={2} className="flex-none" />
          Add workspace
        </Link>
      </div>

      <CardRead read={read} what="workspaces" lines={2}>
        {(list) =>
          list.length === 0 ? (
            <div className="p-3.5">
              <EmptyState
                icon={FolderPlus}
                title="No workspace has been added"
                action={<EmptyStateLink to="/onboarding">Add project folder</EmptyStateLink>}
              >
                The panel finds compose projects only inside the folders added here.
              </EmptyState>
            </div>
          ) : (
            <ul>
              {list.map((w) => (
                <WorkspaceRow key={w.id} workspace={w} onRemove={() => setConfirm(w)} />
              ))}
            </ul>
          )
        }
      </CardRead>

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
    <li className="flex flex-wrap items-center gap-x-5 gap-y-3 border-b border-border px-[18px] py-3.5 last:border-b-0">
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
          className={`${rowButton} border-danger-border text-danger-text`}
        >
          Remove
        </button>
      </div>
    </li>
  )
}
