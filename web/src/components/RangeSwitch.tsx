import { RANGES, type RangeKey } from '../lib/range'

/** RangeSwitch is the chart period switch of designs 02 and 05: 1 hour, 24 hours, 7 days. */
export default function RangeSwitch({
  value,
  onChange,
}: {
  value: RangeKey
  onChange(key: RangeKey): void
}) {
  return (
    <fieldset className="m-0 flex rounded-lg border border-border-strong bg-surface p-[3px]">
      <legend className="sr-only">Time range</legend>
      {RANGES.map((r) => {
        const on = r.key === value
        return (
          <button
            key={r.key}
            type="button"
            aria-pressed={on}
            onClick={() => onChange(r.key)}
            className={`h-7 cursor-pointer rounded-md border-0 px-3 font-sans text-[13px] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent ${
              on ? 'bg-[#262c35] text-white' : 'bg-transparent text-muted hover:text-text'
            }`}
          >
            {r.label}
          </button>
        )
      })}
    </fieldset>
  )
}
