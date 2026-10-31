import { CloudUpload } from 'lucide-react'
import type { Database, Project } from '../../api/types.gen'
import { useLive } from '../../app/live'
import { databaseRule } from '../../lib/backup'
import type { BackupFlow } from '../../lib/useBackup'

const BUTTON =
  'flex h-8 flex-none cursor-pointer items-center gap-[7px] rounded-[7px] border border-border-strong bg-surface-2 px-3 text-[13px] text-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:border-disabled-border disabled:bg-transparent disabled:text-disabled-text'

/**
 * BackUpButton is Back up now for one database (the Database & Backup tab):
 * the same rule as the header, so a Drive reason still opens the Drive
 * dialog, and a spinner while this database's start is out.
 */
export default function BackUpButton({
  project,
  db,
  flow,
}: {
  /** The project as the server last sent it (what the flow takes). */
  project: Project
  db: Database
  flow: BackupFlow
}) {
  const { drive } = useLive()
  const rule = databaseRule(db, drive?.state)
  const pending = flow.pendingProjectId === project.id && flow.pendingKey === db.key
  return (
    <button
      type="button"
      disabled={!rule.enabled || pending}
      aria-busy={pending || undefined}
      title={rule.enabled ? undefined : rule.reason}
      onClick={() => flow.start(project, db.key)}
      className={BUTTON}
    >
      {pending ? (
        <span
          aria-hidden="true"
          className="box-border size-[13px] flex-none rounded-full border-2 border-current border-r-transparent motion-safe:animate-[spin_0.8s_linear_infinite]"
        />
      ) : (
        <CloudUpload aria-hidden="true" size={15} strokeWidth={2} />
      )}
      Back up now
    </button>
  )
}
