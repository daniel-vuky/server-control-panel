import { Database as DatabaseIcon, Folder } from 'lucide-react'
import { Fragment, useId } from 'react'
import type { Backup, Database, Project } from '../../api/types.gen'
import { useLive } from '../../app/live'
import { BackupSteps } from '../../components/BackupProgress'
import { databasePlace, databaseType } from '../../lib/databases'
import type { BackupFlow } from '../../lib/useBackup'
import BackUpButton from './BackUpButton'
import BackupHistory, { useBackupHistory } from './BackupHistory'
import BackupSchedule from './BackupSchedule'
import { useProjectOutlet } from './projectContext'

const card = 'flex flex-col gap-3.5 rounded-[10px] border border-border bg-surface p-[18px]'

/**
 * ProjectDatabase is the Database & Backup tab (design 02): the detected
 * databases, each with Back up now and its running backup, the backup
 * schedule per database and the backup history. The history is read here,
 * since the schedule card shows its copies on Drive.
 */
export default function ProjectDatabase() {
  const { project, raw, backup } = useProjectOutlet()
  const { backups, drive } = useLive()
  const dbId = useId()
  const history = useBackupHistory(raw.id, backup.refusals)
  const folder = drive?.state === 'connected' ? drive.folder : null

  return (
    <div className="flex flex-col gap-4">
      <div className="grid grid-cols-[repeat(auto-fit,minmax(min(360px,100%),1fr))] gap-3.5">
        <section aria-labelledby={dbId} className={card}>
          <h2 id={dbId} className="m-0 text-[15px] font-semibold">
            Detected databases
          </h2>
          {raw.databases.length === 0 ? (
            <p className="m-0 text-[13px] text-muted">No databases detected in this project.</p>
          ) : (
            <>
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
            </>
          )}
        </section>

        <BackupSchedule stored={history.stored} />
      </div>

      <BackupHistory project={raw} flow={backup} history={history} />
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
 * one-line progress that opens the progress dialog.
 */
function DatabaseItem({ db, project, flow, active }: ItemProps) {
  // A SQLite file has no password; every other kind keeps it out of the panel.
  const hasPassword = db.type !== 'sqlite'
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
          <span className="font-mono font-semibold break-all">{db.name}</span>
          <span className="text-[12.5px] break-words text-muted">
            {databaseType(db.type)} · {databasePlace(db)}
            {db.sources.length > 0 && (
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
    </li>
  )
}
