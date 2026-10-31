import type { ReactNode } from 'react'
import { getBackupSettings } from '../../api/backups'
import { getProjectMetrics } from '../../api/metrics'
import type { Project, ProjectPoint, ProjectSeries, TimeZone } from '../../api/types.gen'
import LoadError from '../../components/LoadError'
import MetricChart from '../../components/MetricChart'
import RangeSwitch from '../../components/RangeSwitch'
import { SkeletonCard } from '../../components/Skeleton'
import { formatBytes, formatCpu, formatMem, formatRate } from '../../lib/format'
import { isRangeKey, type RangeKey, useRangeParam } from '../../lib/range'
import { useRead } from '../../lib/useRead'
import { useVisibleInterval } from '../../lib/useVisibleInterval'

const BLUE = '#5AA7FF'
const ORANGE = '#F0913A'
const HEIGHT = 130
/** REFRESH_MS is how often a visible page re-reads (spec decision 24): a sample is taken every 30 s. */
const REFRESH_MS = 30_000

const GRID = 'grid grid-cols-[repeat(auto-fit,minmax(min(320px,100%),1fr))] gap-3.5'

/** sum is a + b, null when either is unknown (as the server sums in + out). */
const sum = (a: number | null, b: number | null) => (a === null || b === null ? null : a + b)

const percentLabel = (v: number) => `${Math.round(v)}%`

/**
 * ProjectCharts is design 02's "App resources" on the Overview tab: the
 * period switch (in the URL, 1 hour by default) and three charts of the
 * project over that period, CPU (Docker's %, 100 = one core), RAM and Network
 * in and out (spec decisions 20 to 23). CPU and RAM read now from the live
 * project, the same numbers as the Dashboard; Network from the newest sample.
 * It re-reads every 30 s while the tab is visible; "History begins" is worded
 * in the server zone.
 */
export default function ProjectCharts({ project }: { project: Project }) {
  const [range, setRange] = useRangeParam('1h')
  const series = useRead(
    (signal) => getProjectMetrics(project.id, range, signal),
    `${project.id}:${range}`,
  )
  // The server zone, as the Backup schedule card reads it.
  const zone = useRead(getBackupSettings, 'zone')

  useVisibleInterval(series.refresh, REFRESH_MS)

  const error = series.error || zone.error
  const retry = () => {
    if (series.data === null) series.refresh()
    if (zone.data === null) zone.refresh()
  }

  let body: ReactNode
  if (series.data && zone.data) {
    body = (
      <Charts
        series={series.data}
        range={range}
        zone={zone.data.timeZone}
        cpu={project.cpuPercent}
        mem={project.memBytes}
      />
    )
  } else if (error !== '') {
    body = <LoadError title="Could not load the charts." message={error} onRetry={retry} />
  } else {
    body = <ChartsLoading />
  }

  return (
    <section aria-labelledby="app-resources" className="flex flex-col gap-[18px]">
      <div className="flex flex-wrap items-center justify-between gap-2.5">
        <h2 id="app-resources" className="m-0 text-[15px] font-semibold">
          App resources
        </h2>
        <RangeSwitch value={range} onChange={setRange} />
      </div>
      {body}
    </section>
  )
}

type ChartsProps = {
  series: ProjectSeries
  range: RangeKey
  zone: TimeZone
  cpu: number | null
  mem: number | null
}

function Charts({ series, range, zone, cpu, mem }: ChartsProps) {
  const { points, latest } = series
  const values = (pick: (p: ProjectPoint) => number | null) => points.map(pick)
  const common = {
    range: isRangeKey(series.range) ? series.range : range,
    historyStart: series.historyStart,
    from: series.from,
    zone,
    height: HEIGHT,
    variant: 'compact' as const,
  }

  return (
    <div className={GRID}>
      <Slot title="CPU">
        <MetricChart
          title="CPU"
          current={formatCpu(cpu)}
          currentWidth={5}
          series={[{ name: 'CPU', values: values((p) => p.cpu), color: BLUE }]}
          formatValue={percentLabel}
          {...common}
        />
      </Slot>
      <Slot title="RAM">
        <MetricChart
          title="RAM"
          current={formatMem(mem)}
          currentWidth={7}
          series={[{ name: 'RAM', values: values((p) => p.mem), color: ORANGE }]}
          formatValue={formatBytes}
          bytes
          {...common}
        />
      </Slot>
      <Slot title="Network">
        <MetricChart
          title="Network"
          current={formatRate(sum(latest?.netIn ?? null, latest?.netOut ?? null))}
          series={[
            { name: 'In', values: values((p) => p.netIn), color: BLUE },
            { name: 'Out', values: values((p) => p.netOut), color: ORANGE, dashed: true },
          ]}
          formatValue={(v) => formatRate(v)}
          bytes
          {...common}
        />
      </Slot>
    </div>
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

/** ChartsLoading holds the charts' place while a period is read. */
function ChartsLoading() {
  return (
    <div aria-busy="true" className={GRID}>
      <span className="sr-only">Loading the charts…</span>
      {[0, 1, 2].map((i) => (
        <SkeletonCard key={i} minHeight="min-h-[220px]" />
      ))}
    </div>
  )
}
