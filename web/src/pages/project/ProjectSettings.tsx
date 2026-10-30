import { type FormEvent, type ReactNode, useEffect, useId, useRef, useState } from 'react'
import { useBlocker } from 'react-router'
import { ApiError, isFieldError } from '../../api/client'
import { saveSettings } from '../../api/projects'
import type { Project, SettingsRequest } from '../../api/types.gen'
import ConfirmDialog from '../../components/ConfirmDialog'
import FieldMessage from '../../components/FieldMessage'
import { useToast } from '../../components/Toast'
import { disabledReason } from '../../lib/status'
import { type ProjectOutlet, useProjectOutlet } from './projectContext'

/** Form is the draft as the inputs hold it: thresholds are text, "" meaning the server default. */
type Form = {
  displayName: string
  publicDomain: string
  stopTemplate: string
  alertCpuPercent: string
  alertRamMb: string
}

type FieldName = keyof Form

const FIELDS: readonly FieldName[] = [
  'displayName',
  'publicDomain',
  'stopTemplate',
  'alertCpuPercent',
  'alertRamMb',
]

const isFieldName = (name: string): name is FieldName => (FIELDS as string[]).includes(name)

const text = (n: number | null) => (n === null ? '' : String(n))
const num = (v: string) => (v.trim() === '' ? null : Number(v))

function formOf(p: Project): Form {
  return {
    displayName: p.displayName,
    publicDomain: p.publicDomain,
    stopTemplate: p.stopTemplate,
    alertCpuPercent: text(p.alertCpuPercent),
    alertRamMb: text(p.alertRamMb),
  }
}

function requestOf(f: Form): SettingsRequest {
  return {
    displayName: f.displayName,
    publicDomain: f.publicDomain,
    stopTemplate: f.stopTemplate,
    alertCpuPercent: num(f.alertCpuPercent),
    alertRamMb: num(f.alertRamMb),
  }
}

const same = (a: Form, b: Form) => FIELDS.every((k) => a[k] === b[k])

const TEMPLATES = [
  { value: 'stop', label: 'docker compose up -d / stop' },
  { value: 'down', label: 'docker compose up -d / down' },
] as const

const mono = (name: string): ReactNode => <span className="font-mono">{name}</span>

const message = (err: unknown) =>
  err instanceof Error && err.message ? err.message : 'Something went wrong.'

/**
 * ProjectSettings is the detail page's Settings tab: the settings form, the
 * unsaved-changes guard and the Stop tracking card. A form per project, so
 * moving to another project's settings starts a fresh draft.
 */
export default function ProjectSettings() {
  const outlet = useProjectOutlet()
  return <SettingsForm key={outlet.raw.id} {...outlet} />
}

function SettingsForm({ project, raw, actions }: ProjectOutlet) {
  const toast = useToast()
  const prefix = useId()
  const idOf = (f: FieldName | 'stack') => `${prefix}-${f}`
  const refs = useRef<Partial<Record<FieldName, HTMLInputElement | HTMLSelectElement | null>>>({})

  // saved is what the server holds: it follows live updates and a save's
  // answer. The draft starts from it and only the user changes it.
  const live = formOf(raw)
  const [seen, setSeen] = useState(live)
  const [saved, setSaved] = useState(live)
  const [draft, setDraft] = useState(live)
  if (!same(seen, live)) {
    setSeen(live)
    setSaved(live)
  }

  const [errors, setErrors] = useState<Partial<Record<FieldName, string>>>({})
  const [saving, setSaving] = useState(false)

  const removed = actions.removedIds.has(raw.id)
  const dirty = !same(draft, saved) && !removed

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

  const set = (f: FieldName, value: string) => {
    setDraft((d) => ({ ...d, [f]: value }))
    setErrors(({ [f]: _gone, ...rest }) => rest)
  }

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault()
    if (!dirty || saving) return
    setSaving(true)
    try {
      const stored = formOf(await saveSettings(raw.id, requestOf(draft)))
      setSaved(stored)
      setDraft(stored)
      setErrors({})
      toast.success(<>Saved settings for {mono(stored.displayName)}</>)
    } catch (err) {
      if (
        err instanceof ApiError &&
        err.status === 422 &&
        isFieldError(err.body) &&
        isFieldName(err.body.field)
      ) {
        const f = err.body.field
        setErrors({ [f]: err.body.error })
        refs.current[f]?.focus()
      } else {
        toast.error(<>Couldn't save settings for {mono(saved.displayName)}</>, message(err))
      }
    } finally {
      setSaving(false)
    }
  }

  /** control is what every input shares: id, ref, value, error state and description. */
  const control = (f: FieldName, notes: string[] = []) => {
    const described = [...notes, ...(errors[f] ? [`${idOf(f)}-error`] : [])]
    return {
      id: idOf(f),
      ref: (el: HTMLInputElement | HTMLSelectElement | null) => {
        refs.current[f] = el
      },
      value: draft[f],
      'aria-invalid': errors[f] ? true : undefined,
      'aria-describedby': described.length > 0 ? described.join(' ') : undefined,
    }
  }
  const border = (f: FieldName) => (errors[f] ? 'border-danger' : 'border-border-strong')
  const error = (f: FieldName) =>
    errors[f] && <FieldMessage id={`${idOf(f)}-error`} message={errors[f]} />

  const templateNote = `${prefix}-template-note`
  const alertsNote = `${prefix}-alerts-note`
  const notFound = project.status === 'notfound'
  const canLeave = project.actions.includes('untrack') || project.actions.includes('remove')
  const leaveLabel = notFound ? 'Remove from list' : 'Stop tracking'
  const cardTitle = `${prefix}-leave-title`

  return (
    <div className="flex max-w-[760px] flex-col gap-4">
      <form
        data-testid="settings-form"
        aria-label="Project settings"
        noValidate
        onSubmit={onSubmit}
        className="grid grid-cols-[repeat(auto-fit,minmax(min(300px,100%),1fr))] gap-4 rounded-[10px] border border-border bg-surface p-5"
      >
        <Field id={idOf('displayName')} label="Display name" error={error('displayName')}>
          <input
            {...control('displayName')}
            type="text"
            autoComplete="off"
            onChange={(e) => set('displayName', e.target.value)}
            className={`${INPUT} ${border('displayName')}`}
          />
        </Field>

        <Field id={idOf('stack')} label="Stack">
          {/* v1 manages docker compose projects only. */}
          <input
            id={idOf('stack')}
            type="text"
            readOnly
            value="docker compose"
            className={`${INPUT} cursor-default border-border-strong text-muted`}
          />
        </Field>

        <Field id={idOf('publicDomain')} label="Public domain" error={error('publicDomain')}>
          <input
            {...control('publicDomain')}
            type="text"
            placeholder="app.example.com"
            spellCheck={false}
            autoComplete="off"
            onChange={(e) => set('publicDomain', e.target.value)}
            className={`${INPUT} font-mono ${border('publicDomain')}`}
          />
        </Field>

        <Field
          id={idOf('stopTemplate')}
          label="Start/stop command (from template)"
          error={error('stopTemplate')}
          note={
            raw.self && (
              <p id={templateNote} className="m-0 text-xs text-muted">
                The panel's own project always stops with stop.
              </p>
            )
          }
        >
          <select
            {...control('stopTemplate', raw.self ? [templateNote] : [])}
            disabled={raw.self}
            onChange={(e) => set('stopTemplate', e.target.value)}
            className={`${INPUT} px-2.5 font-mono text-[13px] disabled:cursor-not-allowed disabled:text-muted ${border('stopTemplate')}`}
          >
            {TEMPLATES.map((t) => (
              <option key={t.value} value={t.value}>
                {t.label}
              </option>
            ))}
          </select>
        </Field>

        <Field
          id={idOf('alertCpuPercent')}
          label="CPU alert threshold (%)"
          error={error('alertCpuPercent')}
        >
          <input
            {...control('alertCpuPercent', [alertsNote])}
            type="number"
            inputMode="numeric"
            min={1}
            max={100}
            step={1}
            placeholder="Server default"
            onChange={(e) => set('alertCpuPercent', e.target.value)}
            className={`${INPUT} font-mono [color-scheme:dark] ${border('alertCpuPercent')}`}
          />
        </Field>

        <Field id={idOf('alertRamMb')} label="RAM alert threshold (MB)" error={error('alertRamMb')}>
          <input
            {...control('alertRamMb', [alertsNote])}
            type="number"
            inputMode="numeric"
            min={1}
            max={1048576}
            step={1}
            placeholder="Server default"
            onChange={(e) => set('alertRamMb', e.target.value)}
            className={`${INPUT} font-mono [color-scheme:dark] ${border('alertRamMb')}`}
          />
        </Field>

        <p id={alertsNote} className="col-span-full -mt-2 mb-0 text-xs text-muted">
          Alerts are not sent yet. The alerts flow will use these.
        </p>

        <div className="col-span-full flex gap-2.5">
          <button
            type="submit"
            disabled={!dirty || saving}
            className="h-[38px] cursor-pointer rounded-[7px] border-0 bg-text px-[18px] font-semibold text-bg focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:bg-border disabled:text-[#5e6672]"
          >
            Save changes
          </button>
        </div>
      </form>

      <section
        aria-labelledby={cardTitle}
        className="flex flex-wrap items-center gap-3.5 rounded-[10px] border border-[#4a1d22] bg-[#160d0f] px-5 py-[18px]"
      >
        <div className="flex min-w-0 flex-[1_1_320px] flex-col gap-1">
          <h2 id={cardTitle} className="m-0 font-semibold text-[#ffd7d8]">
            Stop tracking project
          </h2>
          <p className="m-0 text-[13px] text-[#d4a3a6]">
            Removes {project.displayName} from the tool. Containers, files and backups on Drive are
            not touched.
          </p>
        </div>
        <button
          type="button"
          disabled={!canLeave}
          title={canLeave ? undefined : disabledReason(project.status)}
          onClick={() => actions.leave(raw)}
          className="h-[38px] cursor-pointer rounded-[7px] border border-[#8a2b33] bg-transparent px-4 font-semibold text-[#ff8c8f] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:border-[#3a2226] disabled:text-[#7a5458]"
        >
          {leaveLabel}
        </button>
      </section>

      {blocker.state === 'blocked' && (
        <ConfirmDialog
          title="Discard unsaved changes?"
          body={
            <>
              The settings you changed for{' '}
              <strong className="font-mono">{saved.displayName}</strong> are not saved.
            </>
          }
          confirmLabel="Discard"
          cancelLabel="Keep editing"
          tone="danger"
          onConfirm={() => blocker.proceed()}
          onCancel={() => blocker.reset()}
        />
      )}
    </div>
  )
}

const INPUT =
  'box-border h-[38px] w-full min-w-0 rounded-[7px] border bg-[#0f1215] px-3 text-sm text-text outline-none placeholder:text-muted/60 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent'

type FieldProps = {
  id: string
  label: string
  /** note sits under the control, before any error. */
  note?: ReactNode
  error?: ReactNode
  children: ReactNode
}

/** Field is one labelled cell of the form, per design 02. */
function Field({ id, label, note, error, children }: FieldProps) {
  return (
    <div className="flex min-w-0 flex-col gap-1.5">
      <label htmlFor={id} className="text-[12.5px] text-muted">
        {label}
      </label>
      {children}
      {note}
      {error}
    </div>
  )
}
