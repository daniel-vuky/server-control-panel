import type { ReactNode } from 'react'
import type { ServerInfo, ServerPoint, ServerSeries } from '../../api/types.gen'
import MetricChart, { type ChartSeries } from '../../components/MetricChart'
import { formatBytes, formatPercent, formatRate } from '../../lib/format'
import { isRangeKey } from '../../lib/range'

const BLUE = '#5AA7FF'
const ORANGE = '#F0913A'
const HEIGHT = 180

/** sum is a + b, null when either is unknown (as the server sums in + out). */
const sum = (a: number | null, b: number | null) => (a === null || b === null ? null : a + b)

/** Rate is a point's field in bytes per second. */
type Rate = 'diskRead' | 'diskWrite' | 'netIn' | 'netOut'

const percentLabel = (v: number) => `${Math.round(v)}%`

/**
 * ServerCharts are design 05's four charts (spec decisions 20 to 23): CPU and
 * RAM with their alert threshold, Disk I/O read and write, Network in and out.
 * The current value is the newest sample of the last 90 s, else "—", whatever
 * the period; the time axis is the period the series was read over.
 */
export default function ServerCharts({ series, info }: { series: ServerSeries; info: ServerInfo }) {
  const { points, latest } = series
  const range = isRangeKey(series.range) ? series.range : '24h'
  const values = (pick: (p: ServerPoint) => number | null) => points.map(pick)
  const th = info.thresholds

  const memTotal = latest?.memTotal ?? (info.ramBytes > 0 ? info.ramBytes : null)
  const memPercent =
    latest?.memUsed != null && latest.memTotal ? (latest.memUsed / latest.memTotal) * 100 : null
  const memNow =
    latest?.memUsed != null && latest.memTotal != null
      ? `${formatBytes(latest.memUsed)} / ${formatBytes(latest.memTotal)}`
      : '—'

  const common = {
    range,
    historyStart: series.historyStart,
    from: series.from,
    zone: info.timeZone,
    height: HEIGHT,
  }
  const pair = (a: string, b: string, ka: Rate, kb: Rate): ChartSeries[] => [
    { name: a, values: values((p) => p[ka]), color: BLUE },
    { name: b, values: values((p) => p[kb]), color: ORANGE, dashed: true },
  ]

  return (
    <section
      aria-label="Resource charts"
      className="grid grid-cols-[repeat(auto-fit,minmax(min(460px,100%),1fr))] gap-3.5"
    >
      <Slot title="CPU">
        <MetricChart
          title="CPU"
          current={formatPercent(latest?.cpu ?? null)}
          sub={`average of ${info.cores} vCPU · threshold ${th.cpuPercent}%`}
          series={[{ name: 'CPU', values: values((p) => p.cpu), color: BLUE }]}
          max={100}
          threshold={th.cpuPercent}
          formatValue={percentLabel}
          {...common}
        />
      </Slot>
      <Slot title="RAM">
        <MetricChart
          title="RAM"
          current={memNow}
          sub={`${formatPercent(memPercent)} · threshold ${th.ramPercent}%`}
          series={[{ name: 'Used', values: values((p) => p.memUsed), color: ORANGE }]}
          max={memTotal ?? undefined}
          threshold={memTotal === null ? undefined : (memTotal * th.ramPercent) / 100}
          formatValue={formatBytes}
          bytes
          {...common}
        />
      </Slot>
      <Slot title="Disk I/O">
        <MetricChart
          title="Disk I/O"
          current={formatRate(sum(latest?.diskRead ?? null, latest?.diskWrite ?? null))}
          sub="read + write"
          series={pair('Read', 'Write', 'diskRead', 'diskWrite')}
          formatValue={(v) => formatRate(v)}
          bytes
          {...common}
        />
      </Slot>
      <Slot title="Network">
        <MetricChart
          title="Network"
          current={formatRate(sum(latest?.netIn ?? null, latest?.netOut ?? null))}
          sub="in + out"
          series={pair('In', 'Out', 'netIn', 'netOut')}
          formatValue={(v) => formatRate(v)}
          bytes
          {...common}
        />
      </Slot>
    </section>
  )
}

/** Slot is one cell of the charts grid, named for tests. */
function Slot({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div data-testid={`chart-${title}`} className="min-w-0">
      {children}
    </div>
  )
}
