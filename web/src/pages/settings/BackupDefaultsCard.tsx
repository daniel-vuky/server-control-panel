import { type FormEvent, useEffect, useId, useRef, useState } from 'react'
import { getBackupSettings, saveBackupDefaults } from '../../api/backups'
import { ApiError, isFieldError } from '../../api/client'
import type { BackupSettings } from '../../api/types.gen'
import LoadError from '../../components/LoadError'
import ScheduleForm from '../../components/ScheduleForm'
import { useToast } from '../../components/Toast'
import {
  draftOf,
  fieldsOf,
  isScheduleField,
  type ScheduleDraft,
  type ScheduleField,
  sameDraft,
} from '../../lib/schedule'
import { useLoad } from '../../lib/useLoad'

const message = (err: unknown) =>
  err instanceof Error && err.message ? err.message : 'Something went wrong.'

/**
 * BackupDefaultsCard is Settings → Backup defaults (design 07, without the
 * encryption toggle, with a Day for Weekly): the schedule a newly found
 * database starts from. Saving never changes an existing database's schedule.
 */
export default function BackupDefaultsCard() {
  const { load, retry } = useLoad(getBackupSettings)

  return (
    <section aria-labelledby="bk-title" className="rounded-[10px] border border-border bg-surface">
      <div className="flex flex-col gap-1 border-b border-border px-[18px] py-4">
        <h2 id="bk-title" className="text-[15px] font-semibold">
          Backup defaults
        </h2>
        <span className="text-[12.5px] text-muted">
          Applies to databases found from now on. Each database can still be set on its project's
          Database &amp; Backup tab.
        </span>
      </div>
      {load.state === 'loading' && (
        <p role="status" className="px-[18px] py-3.5 text-[13px] text-muted">
          Loading backup defaults…
        </p>
      )}
      {load.state === 'error' && (
        <div className="p-3.5">
          <LoadError
            title="Could not load backup defaults."
            message={load.message}
            onRetry={retry}
          />
        </div>
      )}
      {load.state === 'ok' && <DefaultsForm settings={load.data} />}
    </section>
  )
}

function DefaultsForm({ settings }: { settings: BackupSettings }) {
  const toast = useToast()
  const prefix = useId()
  const refs = useRef<Partial<Record<ScheduleField, HTMLElement | null>>>({})
  const [saved, setSaved] = useState<ScheduleDraft>(() => draftOf(settings.defaults))
  const [draft, setDraft] = useState<ScheduleDraft>(saved)
  const [errors, setErrors] = useState<Partial<Record<ScheduleField, string>>>({})
  const [saving, setSaving] = useState(false)
  // The field a 422 named: focused once the save ends and the fields unlock.
  const focusNext = useRef<ScheduleField | null>(null)
  const dirty = !sameDraft(draft, saved)

  useEffect(() => {
    if (saving || focusNext.current === null) return
    refs.current[focusNext.current]?.focus()
    focusNext.current = null
  }, [saving])

  const change = (patch: Partial<ScheduleDraft>) => {
    setDraft((d) => ({ ...d, ...patch }))
    setErrors((e) => {
      const next = { ...e }
      for (const k of Object.keys(patch) as ScheduleField[]) delete next[k]
      return next
    })
  }

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault()
    if (!dirty || saving) return
    setSaving(true)
    try {
      const stored = draftOf(await saveBackupDefaults(fieldsOf(draft)))
      setSaved(stored)
      setDraft(stored)
      setErrors({})
      toast.success('Saved backup defaults')
    } catch (err) {
      if (
        err instanceof ApiError &&
        err.status === 422 &&
        isFieldError(err.body) &&
        isScheduleField(err.body.field)
      ) {
        const f = err.body.field
        setErrors({ [f]: err.body.error })
        focusNext.current = f
      } else {
        toast.error("Couldn't save backup defaults", message(err))
      }
    } finally {
      setSaving(false)
    }
  }

  return (
    <form noValidate onSubmit={onSubmit}>
      <div className="px-[18px] pt-4 pb-1">
        <ScheduleForm
          idPrefix={prefix}
          value={draft}
          onChange={change}
          errors={errors}
          zone={settings.timeZone}
          disabled={saving}
          fieldRef={(f, el) => {
            refs.current[f] = el
          }}
        />
      </div>
      <div className="flex justify-end px-[18px] pt-3 pb-[18px]">
        <button
          type="submit"
          disabled={!dirty || saving}
          className="h-[38px] cursor-pointer rounded-[7px] border-0 bg-text px-[18px] font-semibold text-bg focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-60"
        >
          Save changes
        </button>
      </div>
    </form>
  )
}
