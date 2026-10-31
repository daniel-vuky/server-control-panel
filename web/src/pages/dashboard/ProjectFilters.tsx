import { type Filters, STATUS_FILTERS, type StatusFilter, type View } from './filters'

const WORDS: Record<StatusFilter, string> = {
  all: 'All',
  running: 'Running',
  partial: 'Partial',
  stopped: 'Stopped',
  error: 'Error',
}

const FOCUS = 'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent'

const ICON = {
  width: 15,
  height: 15,
  viewBox: '0 0 24 24',
  fill: 'none',
  stroke: 'currentColor',
  strokeWidth: 1.8,
} as const

/**
 * ProjectFilters is the Dashboard's filter bar (design 01 without its Stack
 * select): the status chips with their counts, the name search and the
 * Cards/Table switch. It is controlled: it shows `filters` and reports a change
 * through `onChange`; the page keeps them in the URL.
 */
export default function ProjectFilters({
  filters,
  counts,
  onChange,
}: {
  filters: Filters
  counts: Record<StatusFilter, number>
  onChange(next: Partial<Filters>): void
}) {
  return (
    <div className="flex flex-wrap items-center gap-2.5">
      <fieldset
        aria-label="Filter by status"
        className="m-0 flex min-w-0 flex-wrap gap-1.5 border-0 p-0"
      >
        {STATUS_FILTERS.map((s) => {
          const on = s === filters.status
          return (
            <button
              key={s}
              type="button"
              aria-pressed={on}
              onClick={() => onChange({ status: s })}
              className={`box-border inline-flex h-[34px] cursor-pointer items-center gap-1.5 rounded-[7px] border px-3 font-sans text-[13px] ${FOCUS} ${
                on
                  ? 'border-text bg-text font-semibold text-bg'
                  : 'border-border-strong bg-surface text-text-2 hover:text-text'
              }`}
            >
              {WORDS[s]}
              <span className="min-w-[2ch] text-right font-mono text-xs opacity-75">
                {counts[s]}
              </span>
            </button>
          )
        })}
      </fieldset>
      <label className="flex h-[34px] max-w-[320px] flex-[1_1_200px] items-center gap-2 rounded-[7px] border border-border-strong bg-surface px-2.5">
        <svg
          aria-hidden="true"
          {...ICON}
          strokeWidth={2}
          strokeLinecap="round"
          className="shrink-0 text-muted"
        >
          <circle cx="11" cy="11" r="7" />
          <path d="m20 20-3.5-3.5" />
        </svg>
        <span className="sr-only">Search projects by name</span>
        <input
          type="search"
          value={filters.q}
          placeholder="Search by name…"
          onChange={(e) => onChange({ q: e.target.value })}
          className="min-w-0 flex-1 border-0 bg-transparent font-sans text-[13px] text-text outline-none placeholder:text-muted"
        />
      </label>
      <fieldset
        aria-label="View mode"
        className="m-0 ml-auto flex rounded-lg border border-border-strong bg-surface p-[3px]"
      >
        <ViewButton view="cards" current={filters.view} onChange={onChange}>
          <svg aria-hidden="true" {...ICON}>
            <rect x="3" y="3" width="7" height="7" rx="1" />
            <rect x="14" y="3" width="7" height="7" rx="1" />
            <rect x="3" y="14" width="7" height="7" rx="1" />
            <rect x="14" y="14" width="7" height="7" rx="1" />
          </svg>
          Cards
        </ViewButton>
        <ViewButton view="table" current={filters.view} onChange={onChange}>
          <svg aria-hidden="true" {...ICON} strokeLinecap="round">
            <path d="M4 6h16M4 12h16M4 18h16" />
          </svg>
          Table
        </ViewButton>
      </fieldset>
    </div>
  )
}

function ViewButton({
  view,
  current,
  onChange,
  children,
}: {
  view: View
  current: View
  onChange(next: Partial<Filters>): void
  children: React.ReactNode
}) {
  const on = view === current
  return (
    <button
      type="button"
      aria-pressed={on}
      onClick={() => onChange({ view })}
      className={`inline-flex h-7 cursor-pointer items-center gap-1.5 rounded-md border-0 px-3 font-sans text-[13px] ${FOCUS} ${
        on ? 'bg-selected text-white' : 'bg-transparent text-muted hover:text-text'
      }`}
    >
      {children}
    </button>
  )
}
