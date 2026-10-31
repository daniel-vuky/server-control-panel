import type { ReactNode } from 'react'
import type { TimeZone } from '../api/types.gen'
import {
  FREQUENCIES,
  type ScheduleDraft,
  type ScheduleField,
  WEEKDAYS,
  zoneLabel,
} from '../lib/schedule'
import FieldMessage from './FieldMessage'

const INPUT =
  'box-border h-9 rounded-[7px] border bg-[#0f1215] px-2.5 font-mono text-[13px] text-text [color-scheme:dark] outline-none focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-60'

/** scheduleFieldId is the id of a field's control in a ScheduleForm with this prefix. */
export const scheduleFieldId = (prefix: string, f: ScheduleField) => `${prefix}-${f}`

type Props = {
  /** A unique prefix for the controls' ids (useId). */
  idPrefix: string
  value: ScheduleDraft
  onChange(patch: Partial<ScheduleDraft>): void
  /** The words of a 422 field error, shown under its field. */
  errors: Partial<Record<ScheduleField, string>>
  /** The server zone the run time is read in; null while unknown. */
  zone: TimeZone | null
  disabled?: boolean
  /** fieldRef receives each field's focusable control, so a host can focus the one a 422 names. */
  fieldRef?(f: ScheduleField, el: HTMLElement | null): void
}

/**
 * ScheduleForm is the schedule's fields, shared by Settings → Backup defaults
 * and a database's Backup schedule (designs 02 and 07, no encryption): the
 * Frequency radiogroup, Day for Weekly (Monday first), Run time in the
 * server zone, Copies to keep and Gzip compression. The host owns the draft,
 * the Save button and the errors.
 */
export default function ScheduleForm({
  idPrefix: p,
  value,
  onChange,
  errors,
  zone,
  disabled = false,
  fieldRef,
}: Props) {
  const id = (f: ScheduleField) => scheduleFieldId(p, f)
  const errorId = (f: ScheduleField) => `${id(f)}-error`
  const describe = (f: ScheduleField, notes: string[] = []) => {
    const all = [...notes, ...(errors[f] ? [errorId(f)] : [])]
    return all.length > 0 ? all.join(' ') : undefined
  }
  const border = (f: ScheduleField) => (errors[f] ? 'border-danger' : 'border-border-strong')
  const error = (f: ScheduleField) =>
    errors[f] ? <FieldMessage id={errorId(f)} message={errors[f]} /> : null
  const zoneNote = `${p}-zone`
  const compressNote = `${p}-compress-note`

  return (
    <div className="flex flex-col gap-3.5">
      <div className="flex flex-col gap-1.5">
        <div
          role="radiogroup"
          aria-label="Frequency"
          aria-describedby={describe('frequency')}
          className="flex w-fit rounded-lg border border-border-strong bg-[#0f1215] p-[3px]"
        >
          {FREQUENCIES.map((f) => {
            const checked = value.frequency === f.value
            return (
              <label
                key={f.value}
                className={`flex h-[30px] cursor-pointer items-center rounded-md px-3.5 text-[13px] has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-offset-1 has-[:focus-visible]:outline-accent ${
                  checked ? 'bg-[#262c35] text-white' : 'text-muted'
                }`}
              >
                <input
                  type="radio"
                  name={id('frequency')}
                  value={f.value}
                  checked={checked}
                  disabled={disabled}
                  aria-invalid={errors.frequency ? true : undefined}
                  ref={checked ? (el) => fieldRef?.('frequency', el) : undefined}
                  onChange={() => onChange({ frequency: f.value })}
                  className="sr-only"
                />
                {f.label}
              </label>
            )
          })}
        </div>
        {error('frequency')}
      </div>

      <div className="flex flex-wrap gap-3">
        {value.frequency === 'weekly' && (
          <Field id={id('weekday')} label="Day" error={error('weekday')}>
            <select
              id={id('weekday')}
              value={String(value.weekday)}
              disabled={disabled}
              aria-invalid={errors.weekday ? true : undefined}
              aria-describedby={describe('weekday')}
              ref={(el) => fieldRef?.('weekday', el)}
              onChange={(e) => onChange({ weekday: Number(e.target.value) })}
              className={`${INPUT} w-[150px] font-sans ${border('weekday')}`}
            >
              {WEEKDAYS.map((d) => (
                <option key={d.value} value={d.value}>
                  {d.label}
                </option>
              ))}
            </select>
          </Field>
        )}
        <Field id={id('time')} label="Run time" error={error('time')}>
          <input
            id={id('time')}
            type="time"
            value={value.time}
            disabled={disabled}
            aria-invalid={errors.time ? true : undefined}
            aria-describedby={describe('time', zone ? [zoneNote] : [])}
            ref={(el) => fieldRef?.('time', el)}
            onChange={(e) => onChange({ time: e.target.value })}
            className={`${INPUT} w-[130px] ${border('time')}`}
          />
          {zone && (
            <span id={zoneNote} className="text-[12px] text-muted">
              {zoneLabel(zone)}
            </span>
          )}
        </Field>
        <Field id={id('keep')} label="Copies to keep" error={error('keep')}>
          <input
            id={id('keep')}
            type="number"
            inputMode="numeric"
            min={1}
            max={365}
            step={1}
            value={value.keep}
            disabled={disabled}
            aria-invalid={errors.keep ? true : undefined}
            aria-describedby={describe('keep')}
            ref={(el) => fieldRef?.('keep', el)}
            onChange={(e) => onChange({ keep: e.target.value })}
            className={`${INPUT} w-[110px] ${border('keep')}`}
          />
        </Field>
      </div>

      <div className="flex items-center gap-4 border-t border-[#1a1e24] pt-3">
        <div className="flex flex-1 flex-col gap-[3px]">
          <label htmlFor={id('compress')} className="font-medium">
            Gzip compression
          </label>
          <span id={compressNote} className="text-[12.5px] text-muted">
            Cuts SQL dump size by 70–85%.
          </span>
        </div>
        <span className="relative inline-flex flex-none">
          <input
            id={id('compress')}
            type="checkbox"
            role="switch"
            aria-checked={value.compress}
            checked={value.compress}
            disabled={disabled}
            aria-describedby={describe('compress', [compressNote])}
            ref={(el) => fieldRef?.('compress', el)}
            onChange={(e) => onChange({ compress: e.target.checked })}
            className="peer absolute inset-0 z-10 m-0 size-full cursor-pointer opacity-0 disabled:cursor-not-allowed"
          />
          <span
            aria-hidden="true"
            className={`relative h-[26px] w-11 rounded-full transition-colors duration-200 peer-focus-visible:outline-2 peer-focus-visible:outline-offset-2 peer-focus-visible:outline-accent peer-disabled:opacity-60 ${
              value.compress ? 'bg-switch-on' : 'bg-switch-off'
            }`}
          >
            <span
              className={`absolute top-[3px] size-5 rounded-full bg-white transition-[left] duration-200 ${
                value.compress ? 'left-[21px]' : 'left-[3px]'
              }`}
            />
          </span>
        </span>
      </div>
      {error('compress')}
    </div>
  )
}

function Field({
  id,
  label,
  error,
  children,
}: {
  id: string
  label: string
  error: ReactNode
  children: ReactNode
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={id} className="text-[12.5px] text-muted">
        {label}
      </label>
      {children}
      {error}
    </div>
  )
}
