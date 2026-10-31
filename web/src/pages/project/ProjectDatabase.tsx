import { CalendarClock, Database as DatabaseIcon, Folder } from 'lucide-react'
import { Fragment, useId } from 'react'
import type { Backup, Database, Project } from '../../api/types.gen'
import { useLive } from '../../app/live'
import { BackupSteps } from '../../components/BackupProgress'
import IssueLinks from '../../components/IssueLinks'
import { databasePlace, databaseType } from '../../lib/databases'
import type { BackupFlow } from '../../lib/useBackup'
import BackUpButton from './BackUpButton'
import BackupHistory from './BackupHistory'
import { useProjectOutlet } from './projectContext'

/** SCHEDULE_ISSUES are the issues that bring backup schedules. */
const SCHEDULE_ISSUES = [53]

const card = 'flex flex-col gap-3.5 rounded-[10px] border border-border bg-surface p-[18px]'

/**
 * ProjectDatabase is the Database & Backup tab (design 02): the detected
 * databases, each with Back up now and its running backup, the schedule
 * (an empty state until the next backup flow) and the backup history.
 */
export default function ProjectDatabase() {
  const { project, raw, backup } = useProjectOutlet()
  const { backups, drive } = useLive()
  const dbId = useId()
  const scheduleId = useId()
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

        <section aria-labelledby={scheduleId} className={card}>
          <h2 id={scheduleId} className="m-0 text-[15px] font-semibold">
            Backup schedule
          </h2>
          <div className="flex flex-1 flex-col items-center justify-center gap-2 rounded-lg border border-dashed border-border-strong px-4 py-6 text-center">
            <CalendarClock
              aria-hidden="true"
              size={26}
              strokeWidth={1.4}
              className="text-[#4a515c]"
            />
            <p className="m-0 text-[13px] text-text-2">
              Schedules arrive with the next backup flow.
            </p>
            <p className="m-0 text-[13px] text-muted">
              Coming in <IssueLinks issues={SCHEDULE_ISSUES} />
            </p>
          </div>
        </section>
      </div>

      <BackupHistory project={raw} flow={backup} />
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
    <li className="flex flex-col gap-2.5 rounded-lg border border-border bg-[#0f1215] p-3">
      <div className="flex flex-wrap items-center gap-3">
        <span
          aria-hidden="true"
          className="flex size-9 flex-none items-center justify-center rounded-lg bg-[#172433] text-[#7fb8ff]"
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
          className="flex w-full cursor-pointer items-center rounded-md border border-[#22344a] bg-[#0f141b] px-3 py-2 text-left hover:border-accent focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          <BackupSteps backup={active} compact />
        </button>
      )}
    </li>
  )
}
