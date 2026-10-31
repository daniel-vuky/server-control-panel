import { CloudUpload } from 'lucide-react'
import { useId, useRef, useState } from 'react'
import type { Database, Project } from '../api/types.gen'
import { databaseRule } from '../lib/backup'
import { formatDatabase } from '../lib/databases'
import { plural } from '../lib/plural'
import Dialog, { PRIMARY_BUTTON, SECONDARY_BUTTON } from './Dialog'

type Props = {
  project: Project
  /** initialKey is the database chosen when it opens, when it can be backed up. */
  initialKey: string | null
  driveState: string | undefined
  /** pending is true while the chosen database's backup is being started. */
  pending: boolean
  onCancel(): void
  onConfirm(db: Database): void
}

/**
 * BackupChooser is design 09's Choose DB dialog: one radio row per database
 * (name, type and where it lives). A row that cannot be backed up is disabled
 * with its reason; a Drive reason stays choosable, since it leads to the Drive
 * dialog.
 */
export default function BackupChooser({
  project,
  initialKey,
  driveState,
  pending,
  onCancel,
  onConfirm,
}: Props) {
  const titleId = useId()
  const rowId = useId()
  // Focus starts on the chosen row, so a return from Drive lands on it.
  const chosenRadio = useRef<HTMLInputElement>(null)
  const rows = project.databases.map((db) => ({ db, rule: databaseRule(db, driveState) }))
  const [chosen, setChosen] = useState<string | null>(() => {
    const wanted = rows.find((r) => r.db.key === initialKey && r.rule.enabled)
    return (wanted ?? rows.find((r) => r.rule.enabled))?.db.key ?? null
  })
  const selected = rows.find((r) => r.db.key === chosen && r.rule.enabled) ?? null
  const n = project.databases.length

  return (
    <Dialog labelledBy={titleId} onClose={onCancel} initialFocus={chosenRadio}>
      <h2 id={titleId} className="m-0 text-[17px] font-semibold">
        Backup <span className="font-mono">{project.displayName}</span>
      </h2>
      <p className="m-0 text-muted">
        {n === 1
          ? 'This project has 1 database.'
          : `This project has ${plural(n, 'database', 'databases')}. Choose the one to back up.`}
      </p>
      <fieldset className="m-0 flex flex-col gap-2 border-0 p-0">
        <legend className="sr-only">Database</legend>
        {rows.map(({ db, rule }, i) => {
          const checked = selected?.db.key === db.key
          const descId = `${rowId}-${i}`
          return (
            <label
              key={db.key}
              className={`flex items-center gap-3 rounded-lg border p-3 ${
                checked ? 'border-accent bg-[#0f141b]' : 'border-border-strong'
              } ${rule.enabled ? 'cursor-pointer' : 'cursor-not-allowed opacity-60'}`}
            >
              <input
                ref={checked ? chosenRadio : undefined}
                type="radio"
                name={`${rowId}-db`}
                value={db.key}
                aria-label={db.name}
                aria-describedby={descId}
                checked={checked}
                disabled={!rule.enabled || pending}
                onChange={() => setChosen(db.key)}
                className="size-[18px] flex-none accent-accent"
              />
              <span className="flex min-w-0 flex-col gap-0.5">
                <span className="font-mono font-semibold break-all">{db.name}</span>
                <span id={descId} className="flex flex-col gap-0.5 text-[12.5px] text-muted">
                  <span>{formatDatabase(db)}</span>
                  {rule.reason !== '' && (
                    <span className={rule.enabled ? 'text-muted' : 'text-error-text'}>
                      {rule.reason}
                    </span>
                  )}
                </span>
              </span>
            </label>
          )
        })}
      </fieldset>
      <div className="flex justify-end gap-2.5 pt-1">
        <button type="button" onClick={onCancel} className={SECONDARY_BUTTON}>
          Cancel
        </button>
        <button
          type="button"
          disabled={selected === null || pending}
          aria-busy={pending || undefined}
          onClick={() => selected && onConfirm(selected.db)}
          className={PRIMARY_BUTTON}
        >
          {pending ? (
            <span
              aria-hidden="true"
              className="box-border size-[13px] flex-none rounded-full border-2 border-bg border-r-transparent motion-safe:animate-[spin_0.8s_linear_infinite]"
            />
          ) : (
            <CloudUpload aria-hidden="true" size={15} strokeWidth={2} />
          )}
          Back up now
        </button>
      </div>
    </Dialog>
  )
}
