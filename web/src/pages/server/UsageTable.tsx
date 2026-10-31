import { useState } from 'react'
import { Link } from 'react-router'
import type { UsageReport, UsageRow } from '../../api/types.gen'
import { formatBytes, formatMem, formatPercent, formatRate } from '../../lib/format'
import { statusWord } from '../../lib/status'

type Measure = 'cpu' | 'ram' | 'disk' | 'net'

/** Values are a row's four measures as shown: null is unknown ("—", no bar). */
type Values = Record<Measure, number | null>

type Column = {
  key: Measure
  label: string
  color: string
  format(v: number, row: UsageRow): string
}

/**
 * cpuWord is a share of the server as a whole number (design 05, spec
 * decision 16): "3%", "<1%" for a busy project below 0.5 %, and a known idle
 * 0 as "0%".
 */
const cpuWord = (v: number) => (v > 0 && v < 0.5 ? '<1%' : formatPercent(v))

// Design 05's columns, colours alternating blue and orange.
const COLUMNS: Column[] = [
  { key: 'cpu', label: 'CPU (of server)', color: 'bg-accent', format: cpuWord },
  { key: 'ram', label: 'RAM', color: 'bg-orange', format: (v) => formatMem(v) },
  {
    key: 'disk',
    label: 'Disk',
    color: 'bg-accent',
    format: (v, row) => `${row.diskPartial ? '≥ ' : ''}${formatBytes(v)}`,
  },
  { key: 'net', label: 'Network', color: 'bg-orange', format: (v) => formatRate(v) },
]

const DOT: Record<string, string> = {
  running: 'bg-success',
  partial: 'bg-warning',
  stopped: 'bg-stopped',
  error: 'bg-danger',
  notfound: 'bg-stopped',
}

/**
 * valuesOf are a row's measures. While Docker is down (the live state says so
 * at once) CPU, RAM and network are unknown whatever the last tick read, so
 * the table shows dashes, never stale numbers or zeros.
 */
function valuesOf(row: UsageRow, dockerDown: boolean): Values {
  return {
    cpu: dockerDown ? null : row.cpuPercent,
    ram: dockerDown ? null : row.memBytes,
    disk: row.diskBytes,
    net: dockerDown ? null : row.netBps,
  }
}

/**
 * UsageTable is design 05's Usage by app (spec decisions 15 to 19): the
 * tracked projects and the panel itself, each measure with its share of the
 * server's total as a bar, sorted by the clicked measure (largest first,
 * unknowns last, RAM by default). It scrolls sideways inside its card on a
 * narrow screen.
 */
export default function UsageTable({
  report,
  dockerUp,
}: {
  report: UsageReport
  dockerUp: boolean | null
}) {
  const [sort, setSort] = useState<Measure>('ram')
  const totals: Values = {
    cpu: report.totals.cpuPercent,
    ram: report.totals.memBytes,
    disk: report.totals.diskUsedBytes,
    net: report.totals.netBps,
  }
  const rows = report.rows.map((row) => ({ row, values: valuesOf(row, dockerUp === false) }))
  // Array.prototype.sort is stable: equal values keep the server's order.
  rows.sort((a, b) => {
    const x = a.values[sort]
    const y = b.values[sort]
    if (x === null || y === null) return (x === null ? 1 : 0) - (y === null ? 1 : 0)
    return y - x
  })

  return (
    <section
      aria-labelledby="usage-title"
      className="overflow-x-auto rounded-[10px] border border-border bg-surface"
    >
      <div className="min-w-[900px]">
        <div className="flex flex-wrap items-center justify-between gap-2.5 border-b border-border px-4 py-3.5">
          <h2 id="usage-title" className="m-0 text-[15px] font-semibold">
            Usage by app
          </h2>
          <span className="text-[12.5px] text-muted">
            Bars = share of total server usage · click a column header to sort
          </span>
        </div>
        <table className="w-full table-fixed border-collapse text-left">
          <colgroup>
            <col className="w-[24%]" />
            {COLUMNS.map((c) => (
              <col key={c.key} />
            ))}
          </colgroup>
          <thead>
            <tr className="border-b border-border">
              <th
                scope="col"
                className="px-4 py-1.5 text-xs font-normal tracking-wider text-muted uppercase"
              >
                Project
              </th>
              {COLUMNS.map((c) => {
                const on = c.key === sort
                return (
                  <th
                    key={c.key}
                    scope="col"
                    aria-sort={on ? 'descending' : undefined}
                    className="px-2 py-1.5 last:pr-4"
                  >
                    <button
                      type="button"
                      onClick={() => setSort(c.key)}
                      className={`h-[34px] cursor-pointer border-0 bg-transparent p-0 text-left text-xs tracking-wider uppercase focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent ${
                        on ? 'font-semibold text-white' : 'font-normal text-muted hover:text-text'
                      }`}
                    >
                      {c.label}
                      {on && <span aria-hidden="true"> ↓</span>}
                    </button>
                  </th>
                )
              })}
            </tr>
          </thead>
          <tbody>
            {rows.map(({ row, values }) => (
              <tr
                key={row.projectId ?? `self-${row.name}`}
                className="border-b border-[#1A1E24] last:border-b-0"
              >
                <th scope="row" className="px-4 py-3 font-normal">
                  <AppName row={row} />
                </th>
                {COLUMNS.map((c) => (
                  <td key={c.key} className="px-2 py-3 last:pr-4">
                    <Share
                      value={values[c.key]}
                      total={totals[c.key]}
                      word={(v) => c.format(v, row)}
                      color={c.color}
                      active={c.key === sort}
                    />
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  )
}

/** AppName is the status dot (named for screen readers), the name (a link for a project) and the Panel tag. */
function AppName({ row }: { row: UsageRow }) {
  return (
    <span className="flex min-w-0 items-center gap-2">
      {row.status !== null && (
        <span
          role="img"
          aria-label={statusWord(row.status)}
          className={`size-[7px] flex-none rounded-full ${DOT[row.status] ?? 'bg-warning'}`}
        />
      )}
      {row.projectId !== null ? (
        <Link
          to={`/projects/${row.projectId}`}
          data-testid="app-name"
          className="min-w-0 font-mono font-semibold [overflow-wrap:anywhere] text-white no-underline hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          {row.name}
        </Link>
      ) : (
        <span
          data-testid="app-name"
          className="min-w-0 font-mono font-semibold [overflow-wrap:anywhere] text-white"
        >
          {row.name}
        </span>
      )}
      {row.self && (
        <span className="inline-flex h-5 flex-none items-center rounded-[5px] border border-border-strong px-1.5 text-[11.5px] text-text-2">
          Panel
        </span>
      )}
    </span>
  )
}

/**
 * Share is one measure of a row: the value, its share of the server total
 * (capped at 100 %) and the bar. An unknown value is "—" with no bar; with
 * no known total there is no share to show.
 */
function Share({
  value,
  total,
  word,
  color,
  active,
}: {
  value: number | null
  total: number | null
  word(v: number): string
  color: string
  active: boolean
}) {
  const share =
    value !== null && total !== null && total > 0
      ? Math.min(100, Math.round((value / total) * 100))
      : null
  return (
    <span className="flex flex-col gap-1.5">
      <span className="flex justify-between gap-2 font-mono text-[13px] tabular-nums">
        <span data-testid="usage-value" className="inline-block min-w-[9ch]">
          {value === null ? '—' : word(value)}
        </span>
        {share !== null && (
          <span
            data-testid="usage-share"
            className="inline-block min-w-[4ch] text-right text-muted"
          >
            {share}%
          </span>
        )}
      </span>
      <span aria-hidden="true" className="flex h-1.5 rounded-[3px] bg-[#1A1E24]">
        {share !== null && (
          <span
            data-bar=""
            className={`rounded-[3px] ${color} ${active ? '' : 'opacity-55'}`}
            style={{ width: `${share}%` }}
          />
        )}
      </span>
    </span>
  )
}
