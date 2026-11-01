import { CircleAlert } from 'lucide-react'
import { getServer, getServerMetrics, getServerUsage } from '../../api/metrics'
import type { ServerInfo } from '../../api/types.gen'
import { LiveDim, useLive } from '../../app/live'
import LoadError from '../../components/LoadError'
import PageHeader from '../../components/PageHeader'
import RangeSwitch from '../../components/RangeSwitch'
import Skeleton, { SkeletonCard } from '../../components/Skeleton'
import Stale from '../../components/Stale'
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
 * every 30 s while the tab is visible (a failed re-read keeps what is shown,
 * dimmed under the error banner); the Docker state follows the live state at
 * once.
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
  // Until the server answers, the charts and the system cards hold their place.
  const serverPending = info === null && server.error === ''

  return (
    <div className="flex min-w-0 flex-col gap-5">
      <PageHeader
        title="Server"
        subtitle={
          info ? (
            headerLine(info)
          ) : serverPending ? (
            <span role="status" aria-busy="true" aria-label="Loading the server">
              <Skeleton inline className="h-3 w-[min(420px,70vw)]" />
            </span>
          ) : undefined
        }
        actions={<RangeSwitch value={range} onChange={setRange} />}
      />

      <LiveDim className="flex min-w-0 flex-col gap-5">
        {server.error !== '' && (
          <LoadError
            title="Could not load the server."
            message={server.error}
            onRetry={server.refresh}
            busy={server.pending}
          />
        )}

        {info && !info.available && <NotAvailable reason={info.reason} />}
        {(serverPending || info?.available) && (
          <>
            {series.data && info && (
              <Stale
                error={series.staleError}
                onRetry={series.refresh}
                title="Could not refresh the charts."
              >
                <ServerCharts series={series.data} info={info} />
              </Stale>
            )}
            {(series.data === null || info === null) && series.error === '' && <ChartsLoading />}
            {series.error !== '' && (
              <LoadError
                title="Could not load the charts."
                message={series.error}
                onRetry={series.refresh}
                busy={series.pending}
              />
            )}
          </>
        )}

        {usage.data === null && usage.error === '' && <SkeletonCard />}
        {usage.data && (
          <Stale
            error={usage.staleError}
            onRetry={usage.refresh}
            title="Could not refresh the usage by app."
            className="min-w-0"
          >
            <UsageTable report={usage.data} dockerUp={live.dockerUp} />
          </Stale>
        )}
        {usage.error !== '' && (
          <LoadError
            title="Could not load the usage by app."
            message={usage.error}
            onRetry={usage.refresh}
            busy={usage.pending}
          />
        )}

        {serverPending && (
          <div className={SYSTEM_GRID}>
            <SkeletonCard />
            <SkeletonCard />
          </div>
        )}
        {info && (
          <Stale
            error={server.staleError}
            onRetry={server.refresh}
            title="Could not refresh the server."
            className={SYSTEM_GRID}
          >
            <SystemInfoCard info={info} />
            <ServicesCard info={info} dockerUp={live.dockerUp} />
          </Stale>
        )}
      </LiveDim>
    </div>
  )
}

const SYSTEM_GRID = 'grid grid-cols-[repeat(auto-fit,minmax(min(380px,100%),1fr))] gap-3.5'

/** ChartsLoading holds the charts' place while a period is read. */
function ChartsLoading() {
  return (
    <section
      aria-label="Resource charts"
      aria-busy="true"
      className="grid grid-cols-[repeat(auto-fit,minmax(min(460px,100%),1fr))] gap-3.5"
    >
      {[0, 1, 2, 3].map((i) => (
        <SkeletonCard key={i} minHeight="min-h-[274px]" />
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
