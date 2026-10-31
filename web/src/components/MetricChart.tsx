import type { ReactNode } from 'react'
import type { TimeZone } from '../api/types.gen'
import { niceMax } from '../lib/format'
import { type RangeKey, rangeInfo } from '../lib/range'
import { zoneParts } from '../lib/schedule'

/**
 * ChartSeries is one line of a MetricChart: one value per bucket of the
 * period (null where nothing was sampled), its colour, and whether it is
 * drawn dashed (the second series of a pair).
 */
export type ChartSeries = {
  name: string
  values: (number | null)[]
  color: string
  dashed?: boolean
}

type Props = {
  title: string
  /** current is the value shown big in the header, already worded ("34%", "—"). */
  current: string
  /**
   * currentWidth is the width, in digits (ch), of the box the current value
   * sits in, so a change of its length moves nothing: 5 for a percentage
   * ("1200%"), 7 for a memory size, 9 for a rate ("1023 MB/s", the default),
   * 15 for a used / total pair.
   */
  currentWidth?: keyof typeof CURRENT_WIDTH
  /** sub is the words after the value; a live number in it brings its own slot. */
  sub?: ReactNode
  /** series are one or two lines; a legend key names each. */
  series: ChartSeries[]
  /**
   * max is the scale's top when the data and the threshold stay under it (100
   * for a percentage, the RAM total); above it the scale rounds up from them.
   */
  max?: number
  /** threshold draws a dashed alert line at that value. */
  threshold?: number
  /** formatValue words the scale labels (the top and the middle; the bottom is "0"). */
  formatValue: (v: number) => string
  /** bytes rounds the scale's top in 1024-based units (RAM, disk I/O, network). */
  bytes?: boolean
  range: RangeKey
  /** historyStart is the oldest kept sample (RFC 3339), null with none. */
  historyStart: string | null
  /** from is the start of the period (the first bucket). */
  from: string
  /** zone is the server zone "History begins" is worded in. */
  zone: TimeZone
  /** height is the drawing's height in pixels: 180 on the Server page, 130 on a project. */
  height: number
  /**
   * variant is "full" (the Server page: a y scale, the legend in the header)
   * or "compact" (design 02's project charts: the value on the title's row,
   * no y scale, gridlines at 25, 50 and 75 %, the legend in the footer).
   */
  variant?: 'full' | 'compact'
}

const WIDTH = 600
const PAD = 1

/** The fixed boxes of the header value, one class per width (Tailwind reads whole class names). */
const CURRENT_WIDTH = {
  5: 'min-w-[5ch]',
  7: 'min-w-[7ch]',
  9: 'min-w-[9ch]',
  15: 'min-w-[15ch]',
} as const
const THRESHOLD_COLOR = '#7A2F36'

type Point = [x: number, y: number]

/** sampled says a bucket has a value: null (or anything not a finite number) is a gap. */
const sampled = (v: number | null): v is number => v !== null && Number.isFinite(v)

/** runs splits values into the runs of consecutive samples, each [index, value] pairs. */
function runs(values: (number | null)[]): [number, number][][] {
  const out: [number, number][][] = []
  let run: [number, number][] = []
  values.forEach((v, i) => {
    if (!sampled(v)) {
      if (run.length > 0) out.push(run)
      run = []
      return
    }
    run.push([i, v])
  })
  if (run.length > 0) out.push(run)
  return out
}

const fmt = (n: number) => String(Math.round(n * 10) / 10)
const pointsOf = (pts: Point[]) => pts.map(([x, y]) => `${fmt(x)},${fmt(y)}`).join(' ')

/** historyBegins is "History begins Tue 28 Oct, 14:00" in the server zone, or null for a bad time. */
function historyBegins(iso: string, zone: TimeZone): string | null {
  const p = zoneParts(iso, zone)
  if (p === null) return null
  const [weekday, month, day] = p.day.split(' ')
  return `History begins ${weekday} ${day} ${month}, ${p.time}`
}

/** Legend is the key of each series: its line (dashed for the second of a pair) and its name. */
function Legend({ series, className }: { series: ChartSeries[]; className: string }) {
  return (
    <span data-legend="" className={className}>
      {series.map((s) => (
        <span key={s.name} className="flex items-center gap-1.5">
          <svg aria-hidden="true" width="12" height="2" className="block">
            <line
              x1="0"
              y1="1"
              x2="12"
              y2="1"
              stroke={s.color}
              strokeWidth="2"
              strokeDasharray={s.dashed ? '3 2' : undefined}
            />
          </svg>
          {s.name}
        </span>
      ))}
    </span>
  )
}

/**
 * MetricChart is a hand-drawn line chart of one or two series over a period
 * (designs 05 and 02, no chart library): the title, current value, sub-line
 * and a legend; a y scale (top, middle, 0) whose top is max
 * when it covers the data and the threshold, else a nice value above them; the
 * time axis words of the period (in a grid with the scale, so the axis starts
 * where the drawing does); an area under the first series; a dashed threshold
 * line. The line and its
 * area break where a value is null (a gap, never a zero); a sample alone
 * between two gaps is drawn as a flat segment one bucket wide, centred on it,
 * so it stays visible. When the kept history begins inside the period it says
 * so; when nothing was sampled and no history is kept from before the period,
 * it says there is no data yet. A period with no sample but older history is
 * just a gap (a project that was stopped). The compact variant (design 02)
 * puts the value on the title's row, drops the y scale, draws gridlines at 25,
 * 50 and 75 % and moves the legend into the footer between the period's start
 * and "now"; the notes about history and missing data are the same.
 */
export default function MetricChart({
  title,
  current,
  currentWidth = 9,
  sub,
  series,
  max,
  threshold,
  formatValue,
  bytes = false,
  range,
  historyStart,
  from,
  zone,
  height,
  variant = 'full',
}: Props) {
  const compact = variant === 'compact'
  const info = rangeInfo(range)
  const n = Math.max(0, ...series.map((s) => s.values.length))
  const step = n > 1 ? WIDTH / (n - 1) : WIDTH
  const dataMax = Math.max(0, ...series.flatMap((s) => s.values.filter(sampled)))
  const floor = Math.max(dataMax, threshold ?? 0)
  const top =
    max !== undefined && max > 0 && max >= floor ? max : niceMax(Math.max(floor, max ?? 0), bytes)
  const span = height - 2 * PAD
  const yOf = (v: number) => PAD + (1 - Math.min(Math.max(v, 0), top) / top) * span
  const xOf = (i: number) => (n > 1 ? i * step : WIDTH / 2)

  const empty = series.every((s) => !s.values.some(sampled))
  const startMs = Date.parse(from)
  const historyMs = historyStart === null ? Number.NaN : Date.parse(historyStart)
  const olderHistory = historyMs <= startMs
  const noData = empty && !olderHistory
  const begins =
    !empty && historyStart !== null && historyMs > startMs
      ? historyBegins(historyStart, zone)
      : null

  const names = series.map((s) => s.name)
  const label =
    series.length === 2
      ? `${title} chart, ${names[0]} and ${names[1]}, last ${info.label}`
      : `${title} chart, last ${info.label}`

  const currentValue = (
    <span
      data-testid="chart-current"
      className={`inline-block text-right font-mono text-xl font-semibold tabular-nums ${CURRENT_WIDTH[currentWidth]}`}
    >
      {current}
    </span>
  )

  const gridLine = (y: number, key: string, className = 'stroke-border') => (
    <line
      key={key}
      x1="0"
      y1={y}
      x2={WIDTH}
      y2={y}
      className={className}
      vectorEffect="non-scaling-stroke"
    />
  )

  const drawing = (
    <div className="relative min-w-0">
      <svg
        role="img"
        aria-label={label}
        viewBox={`0 0 ${WIDTH} ${height}`}
        preserveAspectRatio="none"
        className="block w-full"
        style={{ height }}
      >
        {compact ? (
          [0.25, 0.5, 0.75].map((f) => gridLine(height * f, `g${f}`))
        ) : (
          <>
            {gridLine(PAD, 'top')}
            {gridLine(height / 2, 'mid')}
            {gridLine(height - PAD, 'bottom', 'stroke-border-strong')}
          </>
        )}
        {threshold !== undefined && (
          <line
            data-threshold=""
            x1="0"
            y1={fmt(yOf(threshold))}
            x2={WIDTH}
            y2={fmt(yOf(threshold))}
            stroke={THRESHOLD_COLOR}
            strokeDasharray="6 4"
            vectorEffect="non-scaling-stroke"
          />
        )}
        {series.map((s, si) =>
          runs(s.values).map((run) => {
            const pts: Point[] =
              run.length === 1
                ? [
                    [Math.max(0, xOf(run[0][0]) - step / 2), yOf(run[0][1])],
                    [Math.min(WIDTH, xOf(run[0][0]) + step / 2), yOf(run[0][1])],
                  ]
                : run.map(([i, v]) => [xOf(i), yOf(v)])
            const key = `${s.name}-${run[0][0]}`
            return (
              <g key={key}>
                {si === 0 && (
                  <polygon
                    data-area=""
                    points={`${fmt(pts[0][0])},${height} ${pointsOf(pts)} ${fmt(pts[pts.length - 1][0])},${height}`}
                    fill={s.color}
                    fillOpacity={series.length === 1 ? 0.12 : 0.06}
                  />
                )}
                <polyline
                  data-series={s.name}
                  points={pointsOf(pts)}
                  fill="none"
                  stroke={s.color}
                  strokeWidth={s.dashed ? 1.4 : 1.6}
                  strokeDasharray={s.dashed ? '4 3' : undefined}
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  vectorEffect="non-scaling-stroke"
                />
              </g>
            )
          }),
        )}
      </svg>
      {begins && (
        <span className="pointer-events-none absolute top-1.5 left-2 text-[11px] text-muted">
          {begins}
        </span>
      )}
      {noData && (
        <span className="pointer-events-none absolute inset-0 flex items-center justify-center px-4 text-center text-[13px] text-muted">
          No data yet. Samples are taken every 30 seconds.
        </span>
      )}
    </div>
  )

  if (compact) {
    return (
      <div className="flex min-w-0 flex-col gap-2.5 rounded-[10px] border border-border bg-surface p-4">
        <div className="flex items-baseline justify-between gap-2">
          <span className="flex min-w-0 flex-wrap items-baseline gap-x-2 text-[13px] text-muted">
            {title}
            {sub && <span className="text-[12.5px]">{sub}</span>}
          </span>
          {currentValue}
        </div>
        {drawing}
        <div className="flex justify-between gap-2 font-mono text-[11.5px] text-muted">
          <span>{info.start}</span>
          <Legend series={series} className="flex gap-3" />
          <span>now</span>
        </div>
      </div>
    )
  }

  return (
    <div className="flex min-w-0 flex-col gap-2.5 rounded-[10px] border border-border bg-surface px-4 pt-4 pb-3">
      <div className="flex flex-wrap items-baseline gap-x-3.5 gap-y-2">
        <span className="text-sm font-semibold">{title}</span>
        {currentValue}
        {sub && <span className="text-[12.5px] text-muted">{sub}</span>}
        <Legend series={series} className="ml-auto flex gap-3 text-xs text-muted" />
      </div>
      {/* A two-column grid, so the time axis starts where the drawing does
          however wide the scale labels are. */}
      <div className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-2 gap-y-2.5">
        <div
          aria-hidden="true"
          className="flex min-w-10 flex-col justify-between text-right font-mono text-[11px] text-muted"
          style={{ height }}
        >
          <span>{formatValue(top)}</span>
          <span>{formatValue(top / 2)}</span>
          <span>0</span>
        </div>
        {drawing}
        <span aria-hidden="true" />
        <div className="flex justify-between font-mono text-[11px] text-muted">
          <span>{info.start}</span>
          <span>{info.mid}</span>
          <span>now</span>
        </div>
      </div>
    </div>
  )
}
