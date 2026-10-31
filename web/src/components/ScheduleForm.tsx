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
  'box-border rounded-[7px] border bg-field px-2.5 font-mono text-[13px] text-text [color-scheme:dark] outline-none focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-60'

/** The grid layout's controls fill their column at design 07's 38 px. */
const FILL = 'h-[38px] w-full min-w-0'

/** design 07's three equal columns that wrap under 200 px. */
const GRID = 'grid grid-cols-[repeat(auto-fit,minmax(min(200px,100%),1fr))] gap-3.5'

/** GRID_FREQUENCIES is the Schedule select's order (design 07): Daily, Weekly, Off. */
const GRID_FREQUENCIES = [
  ...FREQUENCIES.filter((f) => f.value !== 'off'),
  ...FREQUENCIES.filter((f) => f.value === 'off'),
]

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
  /**
   * segmented (the default) is design 02's radiogroup above wrapping fields;
   * grid is design 07's: Schedule select, Run time and Copies to keep as three
   * equal fields, with Day and the Gzip row under them.
   */
  layout?: 'segmented' | 'grid'
  /** fieldRef receives each field's focusable control, so a host can focus the one a 422 names. */
  fieldRef?(f: ScheduleField, el: HTMLElement | null): void
}

/**
 * ScheduleForm is the schedule's fields, shared by Settings → Backup defaults
 * and a database's Backup schedule (designs 02 and 07, no encryption): the
 * Frequency radiogroup (a Schedule select in the grid layout), Day for Weekly
 * (Monday first), Run time in the server zone, Copies to keep and Gzip
 * compression. The host owns the draft, the Save button and the errors.
 */
export default function ScheduleForm({
  idPrefix: p,
  value,
  onChange,
  errors,
  zone,
  disabled = false,
  layout = 'segmented',
  fieldRef,
}: Props) {
  const grid = layout === 'grid'
  const id = (f: ScheduleField) => scheduleFieldId(p, f)
  const errorId = (f: ScheduleField) => `${id(f)}-error`
  const describe = (f: ScheduleField, notes: string[] = []) => {
    const all = [...notes, ...(errors[f] ? [errorId(f)] : [])]
    return all.length > 0 ? all.join(' ') : undefined
  }
  const border = (f: ScheduleField) => (errors[f] ? 'border-field-error' : 'border-border-strong')
  const error = (f: ScheduleField) =>
    errors[f] ? <FieldMessage id={errorId(f)} message={errors[f]} /> : null
  const zoneNote = `${p}-zone`
  const compressNote = `${p}-compress-note`

  const dayField = (
    <Field
      id={id('weekday')}
      label="Day"
      error={error('weekday')}
      grid={grid}
      className={grid ? 'col-start-1' : undefined}
    >
      <select
        id={id('weekday')}
        value={String(value.weekday)}
        disabled={disabled}
        aria-invalid={errors.weekday ? true : undefined}
        aria-describedby={describe('weekday')}
        ref={(el) => fieldRef?.('weekday', el)}
        onChange={(e) => onChange({ weekday: Number(e.target.value) })}
        className={`${INPUT} ${grid ? FILL : 'h-9 w-[150px]'} font-sans ${border('weekday')}`}
      >
        {WEEKDAYS.map((d) => (
          <option key={d.value} value={d.value}>
            {d.label}
          </option>
        ))}
      </select>
    </Field>
  )

  const frequencySelect = (
    <Field id={id('frequency')} label="Schedule" error={error('frequency')} grid={grid}>
      <select
        id={id('frequency')}
        value={value.frequency}
        disabled={disabled}
        aria-invalid={errors.frequency ? true : undefined}
        aria-describedby={describe('frequency')}
        ref={(el) => fieldRef?.('frequency', el)}
        onChange={(e) => onChange({ frequency: e.target.value as ScheduleDraft['frequency'] })}
        className={`${INPUT} ${FILL} font-sans ${border('frequency')}`}
      >
        {GRID_FREQUENCIES.map((f) => (
          <option key={f.value} value={f.value}>
            {f.label}
          </option>
        ))}
      </select>
    </Field>
  )

  return (
    <div className={`flex flex-col ${grid ? 'gap-4' : 'gap-3.5'}`}>
      {!grid && (
        <div className="flex flex-col gap-1.5">
          <div
            role="radiogroup"
            aria-label="Frequency"
            aria-describedby={describe('frequency')}
            className="flex w-fit rounded-lg border border-border-strong bg-field p-[3px]"
          >
            {FREQUENCIES.map((f) => {
              const checked = value.frequency === f.value
              return (
                <label
                  key={f.value}
                  className={`flex h-[30px] cursor-pointer items-center rounded-md px-3.5 text-[13px] has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-offset-1 has-[:focus-visible]:outline-accent ${
                    checked ? 'bg-selected text-white' : 'text-muted'
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
      )}

      <div className={grid ? GRID : 'flex flex-wrap gap-3'}>
        {grid && frequencySelect}
        {!grid && value.frequency === 'weekly' && dayField}
        <Field id={id('time')} label="Run time" error={error('time')} grid={grid}>
          <input
            id={id('time')}
            type="time"
            value={value.time}
            disabled={disabled}
            aria-invalid={errors.time ? true : undefined}
            aria-describedby={describe('time', zone ? [zoneNote] : [])}
            ref={(el) => fieldRef?.('time', el)}
            onChange={(e) => onChange({ time: e.target.value })}
            className={`${INPUT} ${grid ? FILL : 'h-9 w-[130px]'} ${border('time')}`}
          />
          {zone && (
            <span id={zoneNote} className="text-[12px] text-muted">
              {zoneLabel(zone)}
            </span>
          )}
        </Field>
        <Field id={id('keep')} label="Copies to keep" error={error('keep')} grid={grid}>
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
            className={`${INPUT} ${grid ? FILL : 'h-9 w-[110px]'} ${border('keep')}`}
          />
        </Field>
        {/* Under the three fields: column 1 of the next grid row. */}
        {grid && value.frequency === 'weekly' && dayField}
      </div>

      <div className={`flex items-center gap-4 border-t border-divider ${grid ? 'py-3' : 'pt-3'}`}>
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
  grid = false,
  className = '',
  children,
}: {
  id: string
  label: string
  error: ReactNode
  grid?: boolean
  className?: string
  children: ReactNode
}) {
  return (
    <div className={`flex flex-col gap-1.5 ${grid ? 'min-w-0' : ''} ${className}`.trim()}>
      <label htmlFor={id} className="text-[12.5px] text-muted">
        {label}
      </label>
      {children}
      {error}
    </div>
  )
}
