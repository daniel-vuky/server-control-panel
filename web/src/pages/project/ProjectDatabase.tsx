import { Database as DatabaseIcon, Folder } from 'lucide-react'
import { Fragment, useId, useState } from 'react'
import { ApiError } from '../../api/client'
import { removeDatabase } from '../../api/databases'
import type { Backup, Database, Project } from '../../api/types.gen'
import { useLive } from '../../app/live'
import { BackupSteps } from '../../components/BackupProgress'
import ConfirmDialog from '../../components/ConfirmDialog'
import { SECONDARY_BUTTON } from '../../components/Dialog'
import EmptyState, { EmptyStateButton } from '../../components/EmptyState'
import { useToast } from '../../components/Toast'
import { databasePlace, databaseType } from '../../lib/databases'
import type { BackupFlow } from '../../lib/useBackup'
import AddDatabaseDialog from './AddDatabaseDialog'
import BackUpButton from './BackUpButton'
import BackupHistory, { useBackupHistory } from './BackupHistory'
import BackupSchedule from './BackupSchedule'
import { useProjectOutlet } from './projectContext'

const card = 'flex flex-col gap-3.5 rounded-[10px] border border-border bg-surface p-[18px]'

/**
 * ProjectDatabase is the Database & Backup tab (design 02): the detected
 * databases, each with Back up now and its running backup, the backup
 * schedule per database and the backup history. The history is read here,
 * since the schedule card shows its copies on Drive. A project with none
 * shows design 10's empty card, and either way a database the scan missed is
 * added by hand from the Add database manually dialog.
 */
export default function ProjectDatabase() {
  const { project, raw, backup } = useProjectOutlet()
  const { backups, drive } = useLive()
  const dbId = useId()
  const history = useBackupHistory(raw.id, backup.refusals)
  const folder = drive?.state === 'connected' ? drive.folder : null
  const toast = useToast()
  const [adding, setAdding] = useState(false)

  return (
    <div className="flex flex-col gap-4">
      <div className="grid grid-cols-[repeat(auto-fit,minmax(min(360px,100%),1fr))] gap-3.5">
        <section aria-labelledby={dbId} className={raw.databases.length === 0 ? undefined : card}>
          {raw.databases.length === 0 ? (
            <EmptyState
              icon={DatabaseIcon}
              title="No database detected"
              titleId={dbId}
              action={
                <EmptyStateButton onClick={() => setAdding(true)}>
                  Add database manually
                </EmptyStateButton>
              }
            >
              No DB variables found in .env and no DB service in compose.
            </EmptyState>
          ) : (
            <>
              <h2 id={dbId} className="m-0 text-[15px] font-semibold">
                Detected databases
              </h2>
              <ul className="m-0 flex list-none flex-col gap-2.5 p-0">
                {raw.databases.map((d) => (
                  <DatabaseItem
                    key={d.key}
                    db={d}
                    project={raw}
                    flow={backup}
                    active={backups.find((b) => b.projectId === raw.id && b.database.key === d.key)}
                  />
                ))}
              </ul>
              {folder && (
                <p className="m-0 flex items-center gap-2 text-[13px] text-muted">
                  <Folder aria-hidden="true" size={15} strokeWidth={1.8} className="flex-none" />
                  <span className="min-w-0 break-all">
                    Stored in{' '}
                    <span className="font-mono text-text-2">
                      Drive/{folder.name}/{project.driveFolder}/
                    </span>
                  </span>
                </p>
              )}
              <button
                type="button"
                onClick={() => setAdding(true)}
                className={`${SECONDARY_BUTTON} w-fit`}
              >
                Add database manually
              </button>
            </>
          )}
        </section>

        <BackupSchedule stored={history.stored} />
      </div>

      <BackupHistory project={raw} flow={backup} history={history} />

      {adding && (
        <AddDatabaseDialog
          project={raw}
          onClose={() => setAdding(false)}
          onAdded={(name) => {
            setAdding(false)
            toast.success(`Added database ${name}`)
          }}
        />
      )}
    </div>
  )
}

type ItemProps = {
  db: Database
  project: Project
  flow: BackupFlow
  /** The database's queued or running backup, if any. */
  active: Backup | undefined
}

/**
 * DatabaseItem is one database of design 02: name, type, where it lives and
 * its sources, Back up now, and while a backup of it is queued or running, a
 * one-line progress that opens the progress dialog. A database added by hand
 * has the Added by hand tag instead of sources, and Remove (which keeps its
 * backups on Drive).
 */
function DatabaseItem({ db, project, flow, active }: ItemProps) {
  // A SQLite file has no password; every other kind keeps it out of the panel.
  const hasPassword = db.type !== 'sqlite'
  const toast = useToast()
  const [removing, setRemoving] = useState(false)
  const [busy, setBusy] = useState(false)

  const remove = async () => {
    if (busy) return
    setBusy(true)
    try {
      await removeDatabase(project.id, db.manualId)
      setRemoving(false)
      toast.success(`Removed ${db.name}`)
    } catch (err) {
      // A 409 busy carries the words (a backup of it is queued or running).
      const body = err instanceof ApiError ? (err.body as { message?: unknown } | null) : null
      const detail =
        typeof body?.message === 'string' && body.message !== ''
          ? body.message
          : err instanceof Error && err.message
            ? err.message
            : 'Something went wrong.'
      setRemoving(false)
      toast.error(`Couldn't remove ${db.name}`, detail)
    } finally {
      setBusy(false)
    }
  }
  return (
    <li className="flex flex-col gap-2.5 rounded-lg border border-border bg-field p-3">
      <div className="flex flex-wrap items-center gap-3">
        <span
          aria-hidden="true"
          className="flex size-9 flex-none items-center justify-center rounded-lg bg-[#172433] text-accent-text"
        >
          <DatabaseIcon size={18} strokeWidth={1.8} />
        </span>
        <span className="flex min-w-0 flex-[1_1_180px] flex-col gap-[3px]">
          <span className="flex flex-wrap items-center gap-2">
            <span className="font-mono font-semibold break-all">{db.name}</span>
            {db.manual && (
              <span className="rounded-full border border-border-strong px-2 py-px text-[11.5px] text-muted">
                Added by hand
              </span>
            )}
          </span>
          <span className="text-[12.5px] break-words text-muted">
            {databaseType(db.type)} · {databasePlace(db)}
            {!db.manual && db.sources.length > 0 && (
              <>
                {' · from '}
                {db.sources.map((s, i) => (
                  <Fragment key={s}>
                    {i > 0 && ', '}
                    <span className="font-mono">{s}</span>
                  </Fragment>
                ))}
              </>
            )}
          </span>
        </span>
        {hasPassword && <span className="flex-none text-xs text-muted">Password hidden</span>}
        {db.manual && (
          <button
            type="button"
            aria-label={`Remove ${db.name}`}
            onClick={() => setRemoving(true)}
            className="flex-none cursor-pointer border-0 bg-transparent p-0 text-[13px] text-danger-text underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          >
            Remove
          </button>
        )}
        <BackUpButton project={project} db={db} flow={flow} />
      </div>
      {active && (
        <button
          type="button"
          onClick={() => flow.open(active.id)}
          className="flex w-full cursor-pointer items-center rounded-md border border-info-border bg-info-surface px-3 py-2 text-left hover:border-accent focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          <BackupSteps backup={active} compact />
        </button>
      )}
      {removing && (
        <ConfirmDialog
          title="Remove database"
          body={`Remove ${db.name}? Its backups on Drive are kept.`}
          confirmLabel="Remove"
          tone="danger"
          busy={busy}
          onConfirm={remove}
          onCancel={() => {
            if (!busy) setRemoving(false)
          }}
        />
      )}
    </li>
  )
}
