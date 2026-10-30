import { CloudUpload, Database as DatabaseIcon } from 'lucide-react'
import { Fragment, useId } from 'react'
import type { Database } from '../../api/types.gen'
import IssueLinks from '../../components/IssueLinks'
import { databasePlace, databaseType } from '../../lib/databases'
import { useProjectOutlet } from './projectContext'

/** BACKUP_ISSUES are the E5 issues that bring backups: the epic, Back up now, schedule, history. */
const BACKUP_ISSUES = [6, 48, 53, 56]

/**
 * ProjectDatabase is the Database & Backup tab: the detected databases, read
 * only, and an empty state until the backup flow (E5) arrives.
 */
export default function ProjectDatabase() {
  const { project } = useProjectOutlet()
  const dbId = useId()
  const backupId = useId()

  return (
    <div className="flex flex-col gap-4">
      <section
        aria-labelledby={dbId}
        className="flex flex-col gap-3.5 rounded-[10px] border border-border bg-surface p-[18px]"
      >
        <h2 id={dbId} className="m-0 text-[15px] font-semibold">
          Detected databases
        </h2>
        {project.databases.length === 0 ? (
          <p className="m-0 text-[13px] text-muted">No databases detected in this project.</p>
        ) : (
          <ul className="m-0 flex list-none flex-col gap-2.5 p-0">
            {project.databases.map((d) => (
              <DatabaseItem key={`${d.type}|${d.name}|${d.locationKind}|${d.location}`} db={d} />
            ))}
          </ul>
        )}
      </section>

      <section
        aria-labelledby={backupId}
        className="flex flex-col items-center gap-2.5 rounded-[10px] border border-dashed border-border-strong px-5 py-8 text-center"
      >
        <CloudUpload aria-hidden="true" size={32} strokeWidth={1.4} className="text-[#4a515c]" />
        <h2 id={backupId} className="m-0 text-[15px] font-semibold">
          Backups
        </h2>
        <p className="m-0 text-[13px] text-text-2">Backups arrive with the backup flow.</p>
        <p className="m-0 text-[13px] text-muted">
          Coming in <IssueLinks issues={BACKUP_ISSUES} />
        </p>
      </section>
    </div>
  )
}

/** DatabaseItem is one database of design 02: name, type, where it lives and its sources. */
function DatabaseItem({ db }: { db: Database }) {
  // A SQLite file has no password; every other kind keeps it out of the panel.
  const hasPassword = db.type !== 'sqlite'
  return (
    <li className="flex items-center gap-3 rounded-lg border border-border bg-[#0f1215] p-3">
      <span
        aria-hidden="true"
        className="flex size-9 flex-none items-center justify-center rounded-lg bg-[#172433] text-[#7fb8ff]"
      >
        <DatabaseIcon size={18} strokeWidth={1.8} />
      </span>
      <span className="flex min-w-0 flex-col gap-[3px]">
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
      {hasPassword && <span className="ml-auto flex-none text-xs text-muted">Password hidden</span>}
    </li>
  )
}
