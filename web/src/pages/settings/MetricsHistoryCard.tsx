import { type FormEvent, useEffect, useId, useRef, useState } from 'react'
import { ApiError, isFieldError } from '../../api/client'
import { getMetricsSettings, saveMetricsSettings } from '../../api/metrics'
import FieldMessage from '../../components/FieldMessage'
import LoadError from '../../components/LoadError'
import { useToast } from '../../components/Toast'
import { useLoad } from '../../lib/useLoad'
import { errorMessage } from '../../lib/useRead'

const MIN_DAYS = 7
const MAX_DAYS = 30
/** RANGE_MESSAGE is the server's words for a value outside 7 to 30, said before posting it. */
const RANGE_MESSAGE = 'Enter a whole number of days from 7 to 30.'

/** parseDays is the draft as a whole number of days from 7 to 30, or null. */
function parseDays(draft: string): number | null {
  if (!/^\d+$/.test(draft.trim())) return null
  const n = Number(draft.trim())
  return n >= MIN_DAYS && n <= MAX_DAYS ? n : null
}

/**
 * MetricsHistoryCard is Settings → Metrics history (design 07's card style,
 * spec decision 26): how many days of metrics are kept, 7 to 30. Lowering it
 * deletes the older samples at once.
 */
export default function MetricsHistoryCard() {
  const { load, retry } = useLoad(getMetricsSettings)

  return (
    <section aria-labelledby="mh-title" className="rounded-[10px] border border-border bg-surface">
      <div className="flex flex-col gap-1 border-b border-border px-[18px] py-4">
        <h2 id="mh-title" className="text-[15px] font-semibold">
          Metrics history
        </h2>
        <span className="text-[12.5px] text-muted">
          Older samples are deleted every hour. Charts of 24 hours and 7 days use 5-minute averages.
        </span>
      </div>
      {load.state === 'loading' && (
        <p role="status" className="px-[18px] py-3.5 text-[13px] text-muted">
          Loading metrics history…
        </p>
      )}
      {load.state === 'error' && (
        <div className="p-3.5">
          <LoadError
            title="Could not load metrics history."
            message={load.message}
            onRetry={retry}
          />
        </div>
      )}
      {load.state === 'ok' && <RetentionForm days={load.data.retentionDays} />}
    </section>
  )
}

function RetentionForm({ days }: { days: number }) {
  const toast = useToast()
  const id = useId()
  const errorId = `${id}-error`
  const input = useRef<HTMLInputElement | null>(null)
  const [saved, setSaved] = useState(days)
  const [draft, setDraft] = useState(String(days))
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  // Focus the field once a refused save ends and the field unlocks.
  const focusNext = useRef(false)
  const dirty = draft.trim() !== String(saved)

  useEffect(() => {
    if (saving || !focusNext.current) return
    input.current?.focus()
    focusNext.current = false
  }, [saving])

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault()
    if (!dirty || saving) return
    const value = parseDays(draft)
    if (value === null) {
      setError(RANGE_MESSAGE)
      input.current?.focus()
      return
    }
    setSaving(true)
    try {
      const stored = (await saveMetricsSettings(value)).retentionDays
      setSaved(stored)
      setDraft(String(stored))
      setError('')
      toast.success('Metrics history saved')
    } catch (err) {
      if (
        err instanceof ApiError &&
        err.status === 422 &&
        isFieldError(err.body) &&
        err.body.field === 'retentionDays'
      ) {
        setError(err.body.error)
        focusNext.current = true
      } else {
        toast.error("Couldn't save metrics history", errorMessage(err))
      }
    } finally {
      setSaving(false)
    }
  }

  return (
    <form noValidate onSubmit={onSubmit}>
      <div className="flex flex-col gap-1.5 px-[18px] pt-4 pb-1">
        <label htmlFor={id} className="text-[12.5px] text-muted">
          Keep metrics for
        </label>
        <span
          className={`flex h-[38px] w-[150px] items-center gap-2 rounded-[7px] border bg-[#0f1215] px-3 text-[13px] text-muted focus-within:outline-2 focus-within:outline-offset-2 focus-within:outline-accent ${
            error ? 'border-danger' : 'border-border-strong'
          }`}
        >
          <input
            id={id}
            ref={input}
            type="number"
            inputMode="numeric"
            min={MIN_DAYS}
            max={MAX_DAYS}
            step={1}
            value={draft}
            disabled={saving}
            aria-invalid={error ? true : undefined}
            aria-describedby={error ? errorId : undefined}
            onChange={(e) => {
              setDraft(e.target.value)
              setError('')
            }}
            className="min-w-0 flex-1 border-0 bg-transparent font-mono text-text [color-scheme:dark] outline-none disabled:cursor-not-allowed disabled:opacity-60"
          />
          <span>days</span>
        </span>
        {error && <FieldMessage id={errorId} message={error} />}
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
