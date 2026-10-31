import { type FormEvent, useEffect, useId, useRef, useState } from 'react'
import { useBlocker } from 'react-router'
import { getBackupSettings, saveBackupSchedule } from '../../api/backups'
import { ApiError, isFieldError } from '../../api/client'
import type { Database, DatabaseSchedule, Project, TimeZone } from '../../api/types.gen'
import ConfirmDialog from '../../components/ConfirmDialog'
import FieldMessage from '../../components/FieldMessage'
import ScheduleForm from '../../components/ScheduleForm'
import { useToast } from '../../components/Toast'
import { databaseRule } from '../../lib/backup'
import {
  draftOf,
  fieldsOf,
  formatInZone,
  isScheduleField,
  type ScheduleDraft,
  type ScheduleField,
  sameDraft,
  zoneLabel,
} from '../../lib/schedule'
import { useLoad } from '../../lib/useLoad'
import type { Stored } from './BackupHistory'
import { useProjectOutlet } from './projectContext'

/** SEED is the form of a database with no schedule row yet: the migration's Off row. */
const SEED: DatabaseSchedule = {
  frequency: 'off',
  time: '02:00',
  weekday: 0,
  keep: 14,
  compress: true,
  nextRunAt: null,
}

const message = (err: unknown) =>
  err instanceof Error && err.message ? err.message : 'Something went wrong.'

/** canSchedule says whether a database can ever be backed up, so a schedule means something. */
const canSchedule = (d: Database) => d.backup?.reason !== 'not_supported'

/** Snap is what the server holds for one database: its form and its next run. */
type Snap = { key: string; draft: ScheduleDraft; next: string | null }

const snapOf = (db: Database | undefined): Snap => {
  const s = db?.schedule ?? SEED
  return { key: db?.key ?? '', draft: draftOf(s), next: s.nextRunAt }
}

const sameSnap = (a: Snap, b: Snap) =>
  a.key === b.key && a.next === b.next && sameDraft(a.draft, b.draft)

const OFFSET = /([+-]\d{2}:\d{2})$/

/** offsetOf is the "+07:00" an RFC 3339 text ends with ("+00:00" for Z). */
const offsetOf = (iso: string) => OFFSET.exec(iso)?.[1] ?? '+00:00'

/**
 * nextRunText is "Sun Nov 1, 02:00 (Asia/Ho_Chi_Minh, UTC+07:00)", the time
 * after "Next run: ": the run read in the server zone, never the browser's,
 * with the zone's offset at that run. Until the zone's name is known it reads
 * the offset the server wrote into the time.
 */
function nextRunText(iso: string, zone: TimeZone | null): string {
  const offset = offsetOf(iso)
  const when = formatInZone(iso, zone ?? { name: '', offset })
  const label = zone ? zoneLabel({ name: zone.name, offset }) : `UTC${offset}`
  return `${when} (${label})`
}

type Props = {
  /** The copies on Drive per database key, from the history; null until it answers. */
  stored: Stored
}

/**
 * BackupSchedule is the Backup schedule card of design 02, per database
 * (decision 1): a Database select when several can be scheduled, the shared
 * schedule fields, Save (enabled once something changed; toast "Schedule
 * saved"), the next run in the server zone and "Keeping N · M on Drive". A
 * database that cannot be backed up shows its reason instead of a form.
 * Unsaved changes ask before leaving, as the Settings tab does.
 */
export default function BackupSchedule({ stored }: Props) {
  const { raw, actions } = useProjectOutlet()
  return (
    <ScheduleCard
      key={raw.id}
      project={raw}
      removed={actions.removedIds.has(raw.id)}
      stored={stored}
    />
  )
}

function ScheduleCard({
  project,
  removed,
  stored,
}: {
  project: Project
  removed: boolean
  stored: Stored
}) {
  const toast = useToast()
  const prefix = useId()
  const titleId = `${prefix}-title`
  const selectId = `${prefix}-database`
  const selectErrorId = `${prefix}-database-error`
  const refs = useRef<Partial<Record<ScheduleField, HTMLElement | null>>>({})
  const selectRef = useRef<HTMLSelectElement | null>(null)

  const { load } = useLoad(getBackupSettings)
  const zone = load.state === 'ok' ? load.data.timeZone : null

  const schedulable = project.databases.filter(canSchedule)
  const unsupported = project.databases.filter((d) => !canSchedule(d))
  const [selected, setSelected] = useState(schedulable[0]?.key ?? '')
  const db = schedulable.find((d) => d.key === selected) ?? schedulable[0]

  // saved is what the server holds for the shown database: it follows live
  // updates and a save's answer. The draft follows it while nothing is
  // edited, and starts over when another database is shown.
  const live = snapOf(db)
  const [seen, setSeen] = useState(live)
  const [saved, setSaved] = useState(live)
  const [draft, setDraft] = useState(live.draft)
  const [errors, setErrors] = useState<Partial<Record<ScheduleField, string>>>({})
  const [dbError, setDbError] = useState('')
  if (!sameSnap(seen, live)) {
    const follow = seen.key !== live.key || sameDraft(draft, saved.draft)
    setSeen(live)
    setSaved(live)
    if (follow) {
      setDraft(live.draft)
      setErrors({})
      setDbError('')
    }
  }

  const [saving, setSaving] = useState(false)
  const [pending, setPending] = useState<string | null>(null)
  const dirty = db !== undefined && !sameDraft(draft, saved.draft) && !removed

  const blocker = useBlocker(
    ({ currentLocation, nextLocation }) =>
      dirty && currentLocation.pathname !== nextLocation.pathname,
  )

  // Closing or reloading the browser tab: the browser asks with its own prompt.
  useEffect(() => {
    if (!dirty) return
    const onBeforeUnload = (e: BeforeUnloadEvent) => {
      e.preventDefault()
      e.returnValue = ''
    }
    window.addEventListener('beforeunload', onBeforeUnload)
    return () => window.removeEventListener('beforeunload', onBeforeUnload)
  }, [dirty])

  const change = (patch: Partial<ScheduleDraft>) => {
    setDraft((d) => ({ ...d, ...patch }))
    setErrors((e) => {
      const next = { ...e }
      for (const k of Object.keys(patch) as ScheduleField[]) delete next[k]
      return next
    })
  }

  const choose = (key: string) => {
    if (dirty) setPending(key)
    else setSelected(key)
  }

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault()
    if (!db || !dirty || saving) return
    setSaving(true)
    try {
      const answer = await saveBackupSchedule(project.id, { database: db.key, ...fieldsOf(draft) })
      // The answer is the server's word; one without the database keeps the draft as sent.
      const answered = answer.databases?.find((d) => d.key === db.key)
      const snap = answered ? snapOf(answered) : { key: db.key, draft, next: saved.next }
      setSaved(snap)
      setDraft(snap.draft)
      setErrors({})
      setDbError('')
      toast.success('Schedule saved')
    } catch (err) {
      if (err instanceof ApiError && err.status === 422 && isFieldError(err.body)) {
        const { field, error } = err.body
        if (field === 'database') {
          setDbError(error)
          selectRef.current?.focus()
          return
        }
        if (isScheduleField(field)) {
          setErrors({ [field]: error })
          refs.current[field]?.focus()
          return
        }
      }
      toast.error("Couldn't save the schedule", message(err))
    } finally {
      setSaving(false)
    }
  }

  const several = schedulable.length > 1
  const copies = db && stored !== null ? (stored[db.key] ?? 0) : null

  return (
    <section
      aria-labelledby={titleId}
      className="flex flex-col gap-3.5 rounded-[10px] border border-border bg-surface p-[18px]"
    >
      <h2 id={titleId} className="m-0 text-[15px] font-semibold">
        Backup schedule
      </h2>

      {project.databases.length === 0 && (
        <p className="m-0 text-[13px] text-muted">No databases detected in this project.</p>
      )}

      {db && (
        <form noValidate onSubmit={onSubmit} className="flex flex-col gap-3.5">
          {several && (
            <div className="flex flex-col gap-1.5">
              <label htmlFor={selectId} className="text-[12.5px] text-muted">
                Database
              </label>
              <select
                id={selectId}
                ref={selectRef}
                value={db.key}
                disabled={saving}
                aria-invalid={dbError ? true : undefined}
                aria-describedby={dbError ? selectErrorId : undefined}
                onChange={(e) => choose(e.target.value)}
                className={`box-border h-9 w-full max-w-[280px] rounded-[7px] border bg-field px-2.5 font-mono text-[13px] text-text [color-scheme:dark] outline-none focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-60 ${
                  dbError ? 'border-field-error' : 'border-border-strong'
                }`}
              >
                {schedulable.map((d) => (
                  <option key={d.key} value={d.key}>
                    {d.name}
                  </option>
                ))}
              </select>
              {dbError && <FieldMessage id={selectErrorId} message={dbError} />}
            </div>
          )}
          {!several && dbError && <FieldMessage id={selectErrorId} message={dbError} />}

          <ScheduleForm
            idPrefix={prefix}
            value={draft}
            onChange={change}
            errors={errors}
            zone={zone}
            disabled={saving}
            fieldRef={(f, el) => {
              refs.current[f] = el
            }}
          />

          <div className="flex flex-wrap items-center gap-2.5">
            <button
              type="submit"
              disabled={!dirty || saving}
              aria-busy={saving || undefined}
              className="h-9 cursor-pointer rounded-[7px] border-0 bg-text px-4 font-semibold text-bg focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:bg-border disabled:text-disabled-text"
            >
              Save
            </button>
            <span className="text-[12.5px] text-muted">
              {saved.draft.frequency === 'off' ? (
                'No scheduled backups. Back up now still works.'
              ) : saved.next !== null ? (
                <>
                  Next run:{' '}
                  <span className="font-mono text-text-2">{nextRunText(saved.next, zone)}</span>
                </>
              ) : null}
            </span>
          </div>

          {copies !== null && (
            <p className="m-0 text-[12.5px] text-muted">
              Keeping {saved.draft.keep} · {copies} on Drive
            </p>
          )}
        </form>
      )}

      {unsupported.length > 0 && (
        <ul className="m-0 flex list-none flex-col gap-1.5 p-0 text-[13px] text-muted">
          {unsupported.map((d) => (
            <li key={d.key} className="break-words">
              <span className="font-mono text-text-2">{d.name}</span> ·{' '}
              {databaseRule(d, undefined).reason}
            </li>
          ))}
        </ul>
      )}

      {(blocker.state === 'blocked' || pending !== null) && (
        <ConfirmDialog
          title="Discard unsaved changes?"
          body={
            <>
              The schedule you changed for <strong className="font-mono">{db?.name}</strong> is not
              saved.
            </>
          }
          confirmLabel="Discard"
          cancelLabel="Keep editing"
          tone="danger"
          onConfirm={() => {
            if (blocker.state === 'blocked') blocker.proceed()
            if (pending !== null) {
              // Another key: the draft starts over from that database.
              setSelected(pending)
              setPending(null)
            }
          }}
          onCancel={() => {
            if (blocker.state === 'blocked') blocker.reset()
            setPending(null)
          }}
        />
      )}
    </section>
  )
}
