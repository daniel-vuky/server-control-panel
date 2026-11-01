import { Check, CircleAlert } from 'lucide-react'
import { type FormEvent, type ReactNode, useEffect, useId, useRef, useState } from 'react'
import {
  type AlertChannelName,
  alertFieldError,
  alertWords,
  getAlerts,
  saveAlertThresholds,
  saveEmail,
  saveTelegram,
  setAlertChannel,
  testAlertChannel,
} from '../../api/alerts'
import type { AlertChannel, AlertsConfig, AlertThresholds } from '../../api/types.gen'
import Dialog, { PRIMARY_BUTTON, SECONDARY_BUTTON } from '../../components/Dialog'
import FieldMessage from '../../components/FieldMessage'
import LoadError from '../../components/LoadError'
import { useToast } from '../../components/Toast'
import { useLoad } from '../../lib/useLoad'

const rowButton =
  'flex h-8 cursor-pointer items-center gap-2 rounded-md border border-border-strong bg-transparent px-3 text-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-60'
const INPUT =
  'box-border w-full min-w-0 rounded-[7px] border bg-field px-3 text-[13px] text-text outline-none focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-60'

function Spinner() {
  return (
    <span
      aria-hidden="true"
      className="size-3 animate-spin rounded-full border-2 border-accent border-r-transparent"
    />
  )
}

/**
 * AlertsCard is Settings → Alerts (design 07, spec decisions 3-5, 8 and 10):
 * the Telegram and Email channels with Set up or Edit, Send test and an
 * on/off switch, and the server-wide thresholds.
 */
export default function AlertsCard() {
  const { load, retry } = useLoad(getAlerts)

  return (
    <section
      aria-labelledby="alert-title"
      className="rounded-[10px] border border-border bg-surface"
    >
      <div className="flex flex-col gap-1 border-b border-border px-[18px] py-4">
        <h2 id="alert-title" className="text-[15px] font-semibold">
          Alerts
        </h2>
        <span className="text-[12.5px] text-muted">
          Sent when a project enters Error, a domain fails to respond 3 times in a row, a scheduled
          backup fails, a threshold is exceeded for the set time, or the Tunnel disconnects.
        </span>
      </div>
      {load.state === 'loading' && (
        <p role="status" className="px-[18px] py-3.5 text-[13px] text-muted">
          Loading alert settings…
        </p>
      )}
      {load.state === 'error' && (
        <div className="p-3.5">
          <LoadError
            title="Could not load alert settings."
            message={load.message}
            onRetry={retry}
          />
        </div>
      )}
      {load.state === 'ok' && <AlertsBody initial={load.data} />}
    </section>
  )
}

function AlertsBody({ initial }: { initial: AlertsConfig }) {
  // Every save answers the whole config, so the channels follow each answer.
  const [config, setConfig] = useState(initial)
  const [editing, setEditing] = useState<AlertChannelName | null>(null)

  return (
    <>
      <div className="px-[18px] py-1.5">
        <ChannelRow
          channel="telegram"
          name="Telegram"
          state={config.telegram}
          target={
            config.telegram.configured
              ? `bot ${config.telegram.tokenHint} → chat ${config.telegram.chatId}`
              : ''
          }
          onEdit={() => setEditing('telegram')}
          onChange={setConfig}
        />
        <ChannelRow
          channel="email"
          name="Email"
          state={config.email}
          target={config.email.recipients.join(', ')}
          onEdit={() => setEditing('email')}
          onChange={setConfig}
        />
      </div>
      <ThresholdsForm initial={config.thresholds} />
      {editing === 'telegram' && (
        <TelegramDialog
          configured={config.telegram.configured}
          maskedChat={config.telegram.chatId}
          onClose={() => setEditing(null)}
          onSaved={(next) => {
            setConfig(next)
            setEditing(null)
          }}
        />
      )}
      {editing === 'email' && (
        <EmailDialog
          recipients={config.email.recipients}
          onClose={() => setEditing(null)}
          onSaved={(next) => {
            setConfig(next)
            setEditing(null)
          }}
        />
      )}
    </>
  )
}

type TestResult = { ok: boolean; message: string }

function ChannelRow({
  channel,
  name,
  state,
  target,
  onEdit,
  onChange,
}: {
  channel: AlertChannelName
  name: string
  state: AlertChannel
  target: string
  onEdit(): void
  onChange(next: AlertsConfig): void
}) {
  const toast = useToast()
  const [testing, setTesting] = useState(false)
  const [result, setResult] = useState<TestResult | null>(null)
  const [switching, setSwitching] = useState(false)

  const test = async () => {
    if (testing) return
    setTesting(true)
    setResult(null)
    try {
      const r = await testAlertChannel(channel)
      setResult({ ok: r.ok, message: r.message || (r.ok ? 'Delivered' : 'The test failed.') })
    } catch (err) {
      setResult({ ok: false, message: alertWords(err) })
    } finally {
      setTesting(false)
    }
  }

  const toggle = async () => {
    if (switching) return
    setSwitching(true)
    try {
      onChange(await setAlertChannel(channel, !state.enabled))
    } catch (err) {
      toast.error(`Could not turn alerts via ${name} on or off`, alertWords(err))
    } finally {
      setSwitching(false)
    }
  }

  let detail: ReactNode
  if (!state.available) detail = state.reason
  else if (!state.configured) detail = 'Not set up'
  else detail = target

  return (
    <fieldset
      aria-label={name}
      className="m-0 flex min-w-0 flex-col gap-2 border-x-0 border-t-0 border-b border-divider px-0 py-3 last:border-b-0"
    >
      <div className="flex flex-wrap items-center gap-x-4 gap-y-3">
        <div className="flex flex-[1_1_260px] flex-col gap-[3px]">
          <span className="font-medium">{name}</span>
          <span
            className={`text-[12.5px] text-muted ${state.available && state.configured ? 'font-mono' : ''}`}
          >
            {detail}
          </span>
        </div>
        {state.available && !state.configured && (
          <button type="button" onClick={onEdit} className={rowButton}>
            Set up
          </button>
        )}
        {state.available && state.configured && (
          <>
            <button
              type="button"
              disabled={testing}
              aria-busy={testing || undefined}
              onClick={() => void test()}
              className={rowButton}
            >
              {testing && <Spinner />}
              Send test
            </button>
            <button type="button" onClick={onEdit} className={rowButton}>
              Edit
            </button>
            <button
              type="button"
              role="switch"
              aria-checked={state.enabled}
              aria-label={`Enable alerts via ${name}`}
              disabled={switching}
              onClick={() => void toggle()}
              className={`relative h-[26px] w-11 flex-none cursor-pointer rounded-full border-0 p-0 transition-colors duration-200 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-60 ${state.enabled ? 'bg-switch-on' : 'bg-switch-off'}`}
            >
              <span
                aria-hidden="true"
                className={`absolute top-[3px] size-5 rounded-full bg-white transition-[left] duration-200 ${state.enabled ? 'left-[21px]' : 'left-[3px]'}`}
              />
            </button>
          </>
        )}
      </div>
      {result?.ok === true && (
        <span role="status" className="flex items-center gap-[7px] text-[13px] text-success">
          <Check aria-hidden="true" size={15} strokeWidth={2.4} />
          {result.message}
        </span>
      )}
      {result?.ok === false && (
        <span role="alert" className="flex items-start gap-[7px] text-[13px] text-danger">
          <CircleAlert aria-hidden="true" size={15} strokeWidth={2} className="mt-0.5 flex-none" />
          {result.message}
        </span>
      )}
    </fieldset>
  )
}

/** DialogFrame is the title, the form-level error and the Cancel and Save buttons of a channel dialog. */
function DialogFrame({
  title,
  titleId,
  formError,
  saving,
  onClose,
  onSubmit,
  children,
}: {
  title: string
  titleId: string
  formError: string
  saving: boolean
  onClose(): void
  onSubmit(e: FormEvent): void
  children: ReactNode
}) {
  return (
    <Dialog labelledBy={titleId} onClose={onClose}>
      <h2 id={titleId} className="m-0 text-[17px] font-semibold">
        {title}
      </h2>
      <form noValidate onSubmit={onSubmit} className="flex flex-col gap-3.5">
        {children}
        {formError !== '' && <FieldMessage id={`${titleId}-error`} message={formError} />}
        <div className="flex justify-end gap-2.5 pt-1">
          <button type="button" onClick={onClose} className={SECONDARY_BUTTON}>
            Cancel
          </button>
          <button type="submit" disabled={saving} className={PRIMARY_BUTTON}>
            Save
          </button>
        </div>
      </form>
    </Dialog>
  )
}

/** Labeled is a dialog field: its label, the control, and under it the error or the hint. */
function Labeled({
  id,
  label,
  error,
  hint,
  children,
}: {
  id: string
  label: string
  error?: string
  hint?: string
  children: ReactNode
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={id} className="text-[12.5px] text-muted">
        {label}
      </label>
      {children}
      {error ? (
        <FieldMessage id={`${id}-error`} message={error} />
      ) : (
        hint && (
          <span id={`${id}-hint`} className="text-xs text-muted">
            {hint}
          </span>
        )
      )}
    </div>
  )
}

const TELEGRAM_FIELDS = ['token', 'chatId'] as const
const EMAIL_FIELDS = ['recipients'] as const

/**
 * TelegramDialog sets Telegram up or edits it. Editing, an empty token or an
 * empty chat id keeps the stored one (the masked chat is the placeholder).
 */
function TelegramDialog({
  configured,
  maskedChat,
  onClose,
  onSaved,
}: {
  configured: boolean
  maskedChat: string
  onClose(): void
  onSaved(next: AlertsConfig): void
}) {
  const toast = useToast()
  const ids = useId()
  const tokenId = `${ids}-token`
  const chatId = `${ids}-chat`
  const token = useRef<HTMLInputElement | null>(null)
  const chat = useRef<HTMLInputElement | null>(null)
  const [tokenDraft, setTokenDraft] = useState('')
  const [chatDraft, setChatDraft] = useState('')
  const [errors, setErrors] = useState<{ token?: string; chatId?: string }>({})
  const [formError, setFormError] = useState('')
  const [saving, setSaving] = useState(false)
  // Focus the refused field once the save ends and the fields unlock.
  const focusNext = useRef<(typeof TELEGRAM_FIELDS)[number] | null>(null)

  useEffect(() => {
    if (saving || focusNext.current === null) return
    ;(focusNext.current === 'token' ? token : chat).current?.focus()
    focusNext.current = null
  }, [saving])

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (saving) return
    setSaving(true)
    setErrors({})
    setFormError('')
    try {
      const next = await saveTelegram({ token: tokenDraft.trim(), chatId: chatDraft.trim() })
      toast.success('Telegram saved')
      onSaved(next)
    } catch (err) {
      const field = alertFieldError(err, TELEGRAM_FIELDS)
      if (field !== null) {
        setErrors({ [field.field]: field.error })
        focusNext.current = field.field
      } else {
        setFormError(alertWords(err))
      }
      setSaving(false)
    }
  }

  const titleId = `${ids}-title`
  return (
    <DialogFrame
      title={configured ? 'Edit Telegram' : 'Set up Telegram'}
      titleId={titleId}
      formError={formError}
      saving={saving}
      onClose={onClose}
      onSubmit={(e) => void submit(e)}
    >
      <Labeled
        id={tokenId}
        label="Bot token"
        error={errors.token}
        hint={configured ? 'Leave empty to keep the current token' : undefined}
      >
        <input
          id={tokenId}
          ref={token}
          type="password"
          autoComplete="off"
          spellCheck={false}
          value={tokenDraft}
          disabled={saving}
          aria-invalid={errors.token ? true : undefined}
          aria-describedby={
            errors.token ? `${tokenId}-error` : configured ? `${tokenId}-hint` : undefined
          }
          onChange={(e) => {
            setTokenDraft(e.target.value)
            setErrors((p) => ({ ...p, token: undefined }))
          }}
          className={`${INPUT} h-[38px] font-mono ${errors.token ? 'border-field-error' : 'border-border-strong'}`}
        />
      </Labeled>
      <Labeled
        id={chatId}
        label="Chat ID"
        error={errors.chatId}
        hint={
          configured
            ? 'Leave empty to keep the current chat'
            : 'The numeric id, like -1001234567890.'
        }
      >
        <input
          id={chatId}
          ref={chat}
          type="text"
          inputMode="numeric"
          autoComplete="off"
          placeholder={configured && maskedChat ? maskedChat : undefined}
          value={chatDraft}
          disabled={saving}
          aria-invalid={errors.chatId ? true : undefined}
          aria-describedby={errors.chatId ? `${chatId}-error` : `${chatId}-hint`}
          onChange={(e) => {
            setChatDraft(e.target.value)
            setErrors((p) => ({ ...p, chatId: undefined }))
          }}
          className={`${INPUT} h-[38px] font-mono ${errors.chatId ? 'border-field-error' : 'border-border-strong'}`}
        />
      </Labeled>
    </DialogFrame>
  )
}

function EmailDialog({
  recipients,
  onClose,
  onSaved,
}: {
  recipients: string[]
  onClose(): void
  onSaved(next: AlertsConfig): void
}) {
  const toast = useToast()
  const ids = useId()
  const boxId = `${ids}-recipients`
  const box = useRef<HTMLTextAreaElement | null>(null)
  const [draft, setDraft] = useState(recipients.join('\n'))
  const [error, setError] = useState('')
  const [formError, setFormError] = useState('')
  const [saving, setSaving] = useState(false)
  const focusNext = useRef(false)

  useEffect(() => {
    if (saving || !focusNext.current) return
    box.current?.focus()
    focusNext.current = false
  }, [saving])

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (saving) return
    setSaving(true)
    setError('')
    setFormError('')
    try {
      const next = await saveEmail({ recipients: draft })
      toast.success('Email saved')
      onSaved(next)
    } catch (err) {
      const field = alertFieldError(err, EMAIL_FIELDS)
      if (field !== null) {
        setError(field.error)
        focusNext.current = true
      } else {
        setFormError(alertWords(err))
      }
      setSaving(false)
    }
  }

  const titleId = `${ids}-title`
  return (
    <DialogFrame
      title={recipients.length > 0 ? 'Edit Email' : 'Set up Email'}
      titleId={titleId}
      formError={formError}
      saving={saving}
      onClose={onClose}
      onSubmit={(e) => void submit(e)}
    >
      <Labeled
        id={boxId}
        label="Recipients"
        error={error}
        hint="One address per line, or separated by commas. Up to 10."
      >
        <textarea
          id={boxId}
          ref={box}
          rows={4}
          value={draft}
          disabled={saving}
          spellCheck={false}
          aria-invalid={error ? true : undefined}
          aria-describedby={error ? `${boxId}-error` : `${boxId}-hint`}
          onChange={(e) => {
            setDraft(e.target.value)
            setError('')
          }}
          className={`${INPUT} resize-y py-2 font-mono ${error ? 'border-field-error' : 'border-border-strong'}`}
        />
      </Labeled>
    </DialogFrame>
  )
}

type ThresholdKey = keyof AlertThresholds

const THRESHOLD_FIELDS: ReadonlyArray<{
  key: ThresholdKey
  label: string
  unit: string
  max: number
  words: string
}> = [
  {
    key: 'cpuPercent',
    label: 'CPU threshold',
    unit: '%',
    max: 100,
    words: 'Enter a whole number from 1 to 100.',
  },
  {
    key: 'ramPercent',
    label: 'RAM threshold',
    unit: '%',
    max: 100,
    words: 'Enter a whole number from 1 to 100.',
  },
  {
    key: 'diskPercent',
    label: 'Disk threshold',
    unit: '%',
    max: 100,
    words: 'Enter a whole number from 1 to 100.',
  },
  {
    key: 'sustainedMinutes',
    label: 'Sustained for',
    unit: 'min',
    max: 60,
    words: 'Enter a whole number from 1 to 60.',
  },
]
const THRESHOLD_KEYS = THRESHOLD_FIELDS.map((f) => f.key)

type Draft = Record<ThresholdKey, string>

const draftOf = (t: AlertThresholds): Draft => ({
  cpuPercent: String(t.cpuPercent),
  ramPercent: String(t.ramPercent),
  diskPercent: String(t.diskPercent),
  sustainedMinutes: String(t.sustainedMinutes),
})

/** ThresholdsForm is the grid of server-wide thresholds, checked before posting like the server does. */
function ThresholdsForm({ initial }: { initial: AlertThresholds }) {
  const toast = useToast()
  const ids = useId()
  const refs = useRef<Partial<Record<ThresholdKey, HTMLInputElement | null>>>({})
  const [saved, setSaved] = useState(draftOf(initial))
  const [draft, setDraft] = useState(saved)
  const [errors, setErrors] = useState<Partial<Record<ThresholdKey, string>>>({})
  const [saving, setSaving] = useState(false)
  const focusNext = useRef<ThresholdKey | null>(null)
  const dirty = THRESHOLD_KEYS.some((k) => draft[k].trim() !== saved[k])

  useEffect(() => {
    if (saving || focusNext.current === null) return
    refs.current[focusNext.current]?.focus()
    focusNext.current = null
  }, [saving])

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (!dirty || saving) return
    const bad: Partial<Record<ThresholdKey, string>> = {}
    for (const f of THRESHOLD_FIELDS) {
      const text = draft[f.key].trim()
      const n = Number(text)
      if (!/^\d+$/.test(text) || n < 1 || n > f.max) bad[f.key] = f.words
    }
    const first = THRESHOLD_KEYS.find((k) => bad[k])
    if (first !== undefined) {
      setErrors(bad)
      refs.current[first]?.focus()
      return
    }
    setSaving(true)
    setErrors({})
    try {
      const body = Object.fromEntries(
        THRESHOLD_KEYS.map((k) => [k, Number(draft[k].trim())]),
      ) as unknown as AlertThresholds
      const stored = draftOf((await saveAlertThresholds(body)).thresholds)
      setSaved(stored)
      setDraft(stored)
      toast.success('Alert settings saved')
    } catch (err) {
      const field = alertFieldError(err, THRESHOLD_KEYS)
      if (field !== null) {
        setErrors({ [field.field]: field.error })
        focusNext.current = field.field
      } else {
        toast.error("Couldn't save alert settings", alertWords(err))
      }
    } finally {
      setSaving(false)
    }
  }

  return (
    <form noValidate onSubmit={(e) => void submit(e)}>
      <div className="grid grid-cols-[repeat(auto-fit,minmax(min(180px,100%),1fr))] gap-3.5 px-[18px] pt-3.5">
        {THRESHOLD_FIELDS.map((f) => {
          const id = `${ids}-${f.key}`
          const error = errors[f.key]
          return (
            <div key={f.key} className="flex flex-col gap-1.5">
              <label htmlFor={id} className="text-[12.5px] text-muted">
                {f.label}
              </label>
              <span
                className={`flex h-[38px] items-center gap-2 rounded-[7px] border bg-field px-3 text-[13px] text-muted focus-within:outline-2 focus-within:outline-offset-2 focus-within:outline-accent ${
                  error ? 'border-field-error' : 'border-border-strong'
                }`}
              >
                <input
                  id={id}
                  ref={(el) => {
                    refs.current[f.key] = el
                  }}
                  type="number"
                  inputMode="numeric"
                  min={1}
                  max={f.max}
                  step={1}
                  value={draft[f.key]}
                  disabled={saving}
                  aria-invalid={error ? true : undefined}
                  aria-describedby={error ? `${id}-error` : undefined}
                  onChange={(e) => {
                    setDraft((d) => ({ ...d, [f.key]: e.target.value }))
                    setErrors((p) => ({ ...p, [f.key]: undefined }))
                  }}
                  className="min-w-0 flex-1 border-0 bg-transparent font-mono text-text [color-scheme:dark] outline-none disabled:cursor-not-allowed disabled:opacity-60"
                />
                <span>{f.unit}</span>
              </span>
              {error && <FieldMessage id={`${id}-error`} message={error} />}
            </div>
          )
        })}
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
