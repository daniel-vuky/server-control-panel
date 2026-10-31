import { CircleAlert } from 'lucide-react'
import { getServer, getServerMetrics, getServerUsage } from '../../api/metrics'
import type { ServerInfo } from '../../api/types.gen'
import { LiveDim, useLive } from '../../app/live'
import LoadError from '../../components/LoadError'
import PageHeader from '../../components/PageHeader'
import RangeSwitch from '../../components/RangeSwitch'
import { formatBytes } from '../../lib/format'
import { useRangeParam } from '../../lib/range'
import { useRead } from '../../lib/useRead'
import { useVisibleInterval } from '../../lib/useVisibleInterval'
import ServerCharts from './ServerCharts'
import ServicesCard from './ServicesCard'
import SystemInfoCard, { coresWord, ramWord } from './SystemInfoCard'
import UsageTable from './UsageTable'

/** REFRESH_MS is how often a visible page re-reads (spec decision 24): a sample is taken every 30 s. */
const REFRESH_MS = 30_000

/** rootDisk is the disk the header names: the one at "/", else the fallback row, else the first. */
function rootDisk(info: ServerInfo) {
  return (
    info.disks.find((d) => d.mount === '/') ??
    info.disks.find((d) => d.mount.startsWith('/ ')) ??
    info.disks[0]
  )
}

/** headerLine is design 05's "prod-01 · 4 vCPU · 8 GB RAM · 160 GB disk · sampled every 30 seconds". */
function headerLine(info: ServerInfo): string {
  const disk = rootDisk(info)
  return [
    info.hostname || '—',
    `${coresWord(info.cores)} vCPU`,
    `${ramWord(info.ramBytes)} RAM`,
    ...(disk ? [`${formatBytes(disk.totalBytes)} disk`] : []),
    `sampled every ${info.sampleSeconds} seconds`,
  ].join(' · ')
}

/**
 * ServerPage is /server (design 05): the header with the server's facts and
 * the period switch (in the URL, 24 hours by default), the four resource
 * charts, the usage by app, System info and System services. It re-reads
 * every 30 s while the tab is visible; the Docker state follows the live
 * state at once.
 */
export default function ServerPage() {
  const live = useLive()
  const [range, setRange] = useRangeParam('24h')
  const server = useRead(getServer, 'server')
  // Read alongside the server, but not once it says there are no server metrics.
  const series = useRead(
    (signal) => getServerMetrics(range, signal),
    server.data?.available === false ? null : range,
  )
  const usage = useRead(getServerUsage, 'usage')

  useVisibleInterval(() => {
    server.refresh()
    series.refresh()
    usage.refresh()
  }, REFRESH_MS)

  const info = server.data

  return (
    <div className="flex min-w-0 flex-col gap-5">
      <PageHeader
        title="Server"
        subtitle={info ? headerLine(info) : undefined}
        actions={<RangeSwitch value={range} onChange={setRange} />}
      />

      <LiveDim className="flex min-w-0 flex-col gap-5">
        {info === null && server.error === '' && (
          <p role="status" className="m-0 text-[13px] text-muted">
            Loading the server…
          </p>
        )}
        {server.error !== '' && (
          <LoadError
            title="Could not load the server."
            message={server.error}
            onRetry={server.refresh}
          />
        )}

        {info && !info.available && <NotAvailable reason={info.reason} />}
        {info?.available && series.data && <ServerCharts series={series.data} info={info} />}
        {info?.available && series.data === null && series.error === '' && <ChartsLoading />}
        {info?.available && series.error !== '' && (
          <LoadError
            title="Could not load the charts."
            message={series.error}
            onRetry={series.refresh}
          />
        )}

        {usage.data && <UsageTable report={usage.data} dockerUp={live.dockerUp} />}
        {usage.error !== '' && (
          <LoadError
            title="Could not load the usage by app."
            message={usage.error}
            onRetry={usage.refresh}
          />
        )}

        {info && (
          <div className="grid grid-cols-[repeat(auto-fit,minmax(min(380px,100%),1fr))] gap-3.5">
            <SystemInfoCard info={info} />
            <ServicesCard info={info} dockerUp={live.dockerUp} />
          </div>
        )}
      </LiveDim>
    </div>
  )
}

/** ChartsLoading holds the charts' place while a period is read. */
function ChartsLoading() {
  return (
    <section
      aria-label="Resource charts"
      aria-busy="true"
      className="grid grid-cols-[repeat(auto-fit,minmax(min(460px,100%),1fr))] gap-3.5"
    >
      {[0, 1, 2, 3].map((i) => (
        <div key={i} className="h-[274px] rounded-[10px] border border-border bg-surface p-4">
          <div className="h-3 w-1/4 rounded bg-border motion-safe:animate-pulse" />
        </div>
      ))}
    </section>
  )
}

/** NotAvailable replaces the charts when the host's /proc is not mounted (no server metrics). */
function NotAvailable({ reason }: { reason?: string }) {
  return (
    <div className="flex items-center gap-3 rounded-[10px] border border-border-strong bg-notice px-4 py-3.5 text-[13px] text-text-2">
      <CircleAlert aria-hidden="true" size={18} strokeWidth={1.8} className="flex-none" />
      {reason || "Server metrics are not available: the host's /proc is not mounted."}
    </div>
  )
}
