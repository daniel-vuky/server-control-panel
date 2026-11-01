import { type FormEvent, type ReactNode, useEffect, useId, useRef, useState } from 'react'
import { backupRefusal } from '../../api/backups'
import { ApiError, isFieldError } from '../../api/client'
import { addDatabase } from '../../api/databases'
import type { Project } from '../../api/types.gen'
import Dialog, { PRIMARY_BUTTON, SECONDARY_BUTTON } from '../../components/Dialog'
import FieldMessage from '../../components/FieldMessage'

/** TYPES are the database kinds that can be added by hand, in the order the dialog lists them. */
const TYPES = [
  { value: 'postgresql', label: 'PostgreSQL' },
  { value: 'mysql', label: 'MySQL / MariaDB' },
  { value: 'mongodb', label: 'MongoDB' },
] as const

type DbType = (typeof TYPES)[number]['value']

/** STANDARD_VARS names the variables the image's standard credentials come from, per type. */
const STANDARD_VARS: Record<DbType, string> = {
  postgresql: 'POSTGRES_USER / POSTGRES_PASSWORD',
  mysql: 'MYSQL_USER / MYSQL_PASSWORD (or MARIADB_*, or the root password)',
  mongodb: 'MONGO_INITDB_ROOT_USERNAME / MONGO_INITDB_ROOT_PASSWORD',
}

type Field = 'type' | 'service' | 'name' | 'userVar' | 'passwordVar'
const FIELDS: readonly Field[] = ['type', 'service', 'name', 'userVar', 'passwordVar']
const isField = (f: string): f is Field => (FIELDS as readonly string[]).includes(f)

const INPUT =
  'box-border h-[38px] w-full min-w-0 rounded-[7px] border bg-field px-2.5 text-[13px] text-text [color-scheme:dark] outline-none focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-60'

const message = (err: unknown) =>
  err instanceof Error && err.message ? err.message : 'Something went wrong.'

/** timedOut is a request that the time limit (or an abort) ended before any answer. */
const timedOut = (err: unknown) => {
  const name = (err as { name?: unknown } | null)?.name
  return name === 'TimeoutError' || name === 'AbortError'
}

const MSG_TIMEOUT = 'The check did not answer in time. Try again.'
const MSG_NO_SERVICES = "This project's compose config could not be read. Rescan first."

type Props = {
  project: Project
  /** onAdded runs once the database is stored; the live state carries the new list. */
  onAdded(name: string): void
  onClose(): void
}

/**
 * AddDatabaseDialog is design 09's dialog for a database the scan did not
 * find (#58): its type, the compose service it runs in, its name and where the
 * credentials are, as the names of variables read inside the container (the
 * panel never holds a value). Check and add runs a check in the container;
 * a failed check, a service that does not run, a 422 on a field (focused once
 * the form is enabled again) and a request with no answer in 35 s show here
 * and nothing is saved. A project whose compose config could not be read has
 * no services to offer, so Check and add stays disabled.
 */
export default function AddDatabaseDialog({ project, onAdded, onClose }: Props) {
  const prefix = useId()
  const titleId = `${prefix}-title`
  const id = (f: string) => `${prefix}-${f}`
  const nameRef = useRef<HTMLInputElement>(null)
  const refs = useRef<Partial<Record<Field, HTMLElement | null>>>({})

  const services = project.serviceNames
  const noServices = services.length === 0
  const [type, setType] = useState<DbType>('postgresql')
  const [service, setService] = useState(services[0] ?? '')
  const [name, setName] = useState('')
  const [custom, setCustom] = useState(false)
  const [userVar, setUserVar] = useState('')
  const [passwordVar, setPasswordVar] = useState('')
  const [errors, setErrors] = useState<Partial<Record<Field, string>>>({})
  const [formError, setFormError] = useState('')
  const [busy, setBusy] = useState(false)
  // Set by fail: focus the first field in error once the inputs are enabled
  // again (after a server answer they are still disabled when fail runs).
  const focusPending = useRef(false)

  useEffect(() => {
    if (!focusPending.current) return
    focusPending.current = false
    const first = FIELDS.find((f) => errors[f])
    if (first) refs.current[first]?.focus()
  }, [errors])

  const setRef = (f: Field) => (el: HTMLElement | null) => {
    refs.current[f] = el
  }

  const clear = (...fields: Field[]) => {
    setErrors((e) => {
      const next = { ...e }
      for (const f of fields) delete next[f]
      return next
    })
    setFormError('')
  }

  const fail = (next: Partial<Record<Field, string>>) => {
    focusPending.current = true
    setErrors(next)
  }

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault()
    if (busy || noServices) return
    const trimmed = name.trim()
    const user = custom ? userVar.trim() : ''
    const pass = custom ? passwordVar.trim() : ''
    const local: Partial<Record<Field, string>> = {}
    if (service === '' || !services.includes(service)) {
      local.service = "Choose one of the project's services."
    }
    if (trimmed === '') local.name = 'Enter the database name.'
    if (custom && user === '') local.userVar = 'Enter the user variable.'
    if (custom && pass === '') local.passwordVar = 'Enter the password variable.'
    setFormError('')
    if (Object.keys(local).length > 0) {
      fail(local)
      return
    }
    setErrors({})
    setBusy(true)
    try {
      await addDatabase(project.id, {
        type,
        service,
        name: trimmed,
        userVar: user,
        passwordVar: pass,
      })
      onAdded(trimmed)
    } catch (err) {
      setBusy(false)
      if (timedOut(err)) {
        setFormError(MSG_TIMEOUT)
        return
      }
      if (err instanceof ApiError && err.status === 422 && isFieldError(err.body)) {
        const { field, error } = err.body
        if (isField(field)) fail({ [field]: error })
        else setFormError(error)
        return
      }
      if (err instanceof ApiError && err.status === 409) {
        const refusal = backupRefusal(err)
        if (refusal?.error === 'db_not_running') {
          setFormError(`Start ${refusal.service || service} first`)
        } else {
          setFormError(refusal?.message || message(err))
        }
        return
      }
      if (err instanceof ApiError && err.status === 503) {
        const body = err.body as { message?: unknown } | null
        setFormError(
          typeof body?.message === 'string' && body.message !== ''
            ? body.message
            : 'Docker is not responding.',
        )
        return
      }
      setFormError(message(err))
    }
  }

  const border = (f: Field) => (errors[f] ? 'border-field-error' : 'border-border-strong')
  const describe = (f: Field) => (errors[f] ? `${id(f)}-error` : undefined)
  const error = (f: Field) =>
    errors[f] ? <FieldMessage id={`${id(f)}-error`} message={errors[f]} /> : null

  return (
    <Dialog
      labelledBy={titleId}
      onClose={() => {
        if (!busy) onClose()
      }}
      initialFocus={nameRef}
      width="w-[480px]"
    >
      <h2 id={titleId} className="m-0 text-[17px] font-semibold">
        Add database manually
      </h2>
      <p className="m-0 leading-[1.55] text-muted">
        For a database the scan did not find. It runs in a service of{' '}
        <span className="font-mono text-text-2">{project.displayName}</span>.
      </p>
      <form noValidate onSubmit={onSubmit} className="flex flex-col gap-3.5">
        <div className="flex flex-col gap-1.5">
          <span id={id('type')} className="text-[12.5px] text-muted">
            Type
          </span>
          <div
            role="radiogroup"
            aria-labelledby={id('type')}
            className="flex w-fit max-w-full flex-wrap rounded-lg border border-border-strong bg-field p-[3px]"
          >
            {TYPES.map((t) => {
              const checked = type === t.value
              return (
                <label
                  key={t.value}
                  className={`flex h-[30px] cursor-pointer items-center rounded-md px-3.5 text-[13px] has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-offset-1 has-[:focus-visible]:outline-accent ${
                    checked ? 'bg-selected text-white' : 'text-muted'
                  }`}
                >
                  <input
                    type="radio"
                    name={id('type')}
                    value={t.value}
                    checked={checked}
                    disabled={busy}
                    ref={checked ? setRef('type') : undefined}
                    onChange={() => {
                      setType(t.value)
                      clear('type')
                    }}
                    className="sr-only"
                  />
                  {t.label}
                </label>
              )
            })}
          </div>
          {error('type')}
        </div>

        <div className="flex flex-col gap-1.5">
          <label htmlFor={id('service')} className="text-[12.5px] text-muted">
            Service
          </label>
          <select
            id={id('service')}
            value={service}
            disabled={busy}
            aria-invalid={errors.service ? true : undefined}
            aria-describedby={noServices ? id('no-services') : describe('service')}
            ref={setRef('service')}
            onChange={(e) => {
              setService(e.target.value)
              clear('service')
            }}
            className={`${INPUT} font-mono ${border('service')}`}
          >
            {services.length === 0 && <option value="">No services</option>}
            {services.map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </select>
          {error('service')}
          {noServices && <FieldMessage id={id('no-services')} message={MSG_NO_SERVICES} />}
        </div>

        <div className="flex flex-col gap-1.5">
          <label htmlFor={id('name')} className="text-[12.5px] text-muted">
            Database name
          </label>
          <input
            id={id('name')}
            ref={(el) => {
              nameRef.current = el
              setRef('name')(el)
            }}
            type="text"
            value={name}
            disabled={busy}
            autoComplete="off"
            spellCheck={false}
            aria-invalid={errors.name ? true : undefined}
            aria-describedby={describe('name')}
            onChange={(e) => {
              setName(e.target.value)
              clear('name')
            }}
            className={`${INPUT} font-mono ${border('name')}`}
          />
          {error('name')}
        </div>

        <fieldset className="m-0 flex flex-col gap-2 border-0 p-0">
          <legend className="mb-1.5 p-0 text-[12.5px] text-muted">Credentials</legend>
          <CredentialChoice
            checked={!custom}
            disabled={busy}
            name={id('credentials')}
            onChoose={() => {
              setCustom(false)
              clear('userVar', 'passwordVar')
            }}
            label="The image's standard variables"
            note={<span className="font-mono">{STANDARD_VARS[type]}</span>}
          />
          <CredentialChoice
            checked={custom}
            disabled={busy}
            name={id('credentials')}
            onChoose={() => setCustom(true)}
            label="Other variables"
          />
          {custom && (
            <div className="grid grid-cols-[repeat(auto-fit,minmax(min(180px,100%),1fr))] gap-3 pl-[30px]">
              <div className="flex flex-col gap-1.5">
                <label htmlFor={id('userVar')} className="text-[12.5px] text-muted">
                  User variable
                </label>
                <input
                  id={id('userVar')}
                  ref={setRef('userVar')}
                  type="text"
                  value={userVar}
                  disabled={busy}
                  autoComplete="off"
                  spellCheck={false}
                  placeholder="APP_USER"
                  aria-invalid={errors.userVar ? true : undefined}
                  aria-describedby={describe('userVar')}
                  onChange={(e) => {
                    setUserVar(e.target.value)
                    clear('userVar')
                  }}
                  className={`${INPUT} font-mono ${border('userVar')}`}
                />
                {error('userVar')}
              </div>
              <div className="flex flex-col gap-1.5">
                <label htmlFor={id('passwordVar')} className="text-[12.5px] text-muted">
                  Password variable
                </label>
                <input
                  id={id('passwordVar')}
                  ref={setRef('passwordVar')}
                  type="text"
                  value={passwordVar}
                  disabled={busy}
                  autoComplete="off"
                  spellCheck={false}
                  placeholder="APP_PASSWORD"
                  aria-invalid={errors.passwordVar ? true : undefined}
                  aria-describedby={describe('passwordVar')}
                  onChange={(e) => {
                    setPasswordVar(e.target.value)
                    clear('passwordVar')
                  }}
                  className={`${INPUT} font-mono ${border('passwordVar')}`}
                />
                {error('passwordVar')}
              </div>
            </div>
          )}
        </fieldset>

        <p className="m-0 text-[12.5px] leading-normal text-muted">
          The panel never asks for the password: it is read inside the container.
        </p>

        {formError && <FieldMessage id={id('form-error')} message={formError} />}

        <div className="flex justify-end gap-2.5 pt-1">
          <button type="button" disabled={busy} onClick={onClose} className={SECONDARY_BUTTON}>
            Cancel
          </button>
          <button
            type="submit"
            disabled={busy || noServices}
            aria-busy={busy || undefined}
            className={PRIMARY_BUTTON}
          >
            {busy && (
              <span
                aria-hidden="true"
                className="box-border size-[13px] flex-none rounded-full border-2 border-bg border-r-transparent motion-safe:animate-[spin_0.8s_linear_infinite]"
              />
            )}
            {busy ? 'Checking…' : 'Check and add'}
          </button>
        </div>
      </form>
    </Dialog>
  )
}

/** CredentialChoice is one radio row of the Credentials group: a label and an optional note under it. */
function CredentialChoice({
  checked,
  disabled,
  name,
  onChoose,
  label,
  note,
}: {
  checked: boolean
  disabled: boolean
  name: string
  onChoose(): void
  label: string
  note?: ReactNode
}) {
  return (
    <label
      className={`flex cursor-pointer items-start gap-3 rounded-lg border p-3 ${
        checked ? 'border-accent bg-info-surface' : 'border-border-strong'
      } ${disabled ? 'cursor-not-allowed opacity-60' : ''}`}
    >
      <input
        type="radio"
        name={name}
        checked={checked}
        disabled={disabled}
        onChange={onChoose}
        className="mt-0.5 size-[18px] flex-none accent-accent"
      />
      <span className="flex min-w-0 flex-col gap-0.5">
        <span className="font-medium">{label}</span>
        {note && <span className="text-[12.5px] break-words text-muted">{note}</span>}
      </span>
    </label>
  )
}
