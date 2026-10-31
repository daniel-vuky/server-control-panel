import { getServer, getServerMetrics } from '../../api/metrics'
import Skeleton from '../../components/Skeleton'
import Sparkline from '../../components/Sparkline'
import { useRead } from '../../lib/useRead'
import { useVisibleInterval } from '../../lib/useVisibleInterval'
import { summaryTiles, type Tile, tileFill } from './summary'

/** REFRESH_MS is how often a visible page re-reads: a sample is taken every 30 s. */
const REFRESH_MS = 30_000

const FLAG = {
  ok: 'bg-[rgba(63,207,142,0.12)] text-success',
  bad: 'bg-[rgba(242,85,90,0.16)] text-danger-text',
}

/**
 * ServerSummary is the six tiles at the top of the Dashboard (design 01): CPU,
 * RAM, Disk, Load avg, Uptime and CF Tunnel, each with its last-hour chart. It
 * reads /api/server and the last hour of /api/server/metrics, shows skeletons
 * until both have answered once (or either has failed), and re-reads both
 * every 30 s while the tab is visible. A failed read leaves its tiles at "—".
 */
export default function ServerSummary() {
  const server = useRead(getServer, 'server')
  const series = useRead((signal) => getServerMetrics('1h', signal), '1h')

  useVisibleInterval(() => {
    server.refresh()
    series.refresh()
  }, REFRESH_MS)

  const failed = server.error !== '' || series.error !== ''
  const loading = !failed && (server.data === null || series.data === null)

  return (
    <section
      aria-label="Server summary"
      aria-busy={loading ? 'true' : undefined}
      className="grid grid-cols-[repeat(auto-fit,minmax(min(180px,100%),1fr))] gap-3"
    >
      {loading
        ? [0, 1, 2, 3, 4, 5].map((i) => <TileSkeleton key={i} />)
        : summaryTiles(server.data, series.data, new Date()).map((t) => (
            <TileView key={t.key} tile={t} />
          ))}
    </section>
  )
}

/** TileView is one tile: label and flag, the value and its sub-line, then the chart. */
function TileView({ tile: t }: { tile: Tile }) {
  return (
    <article
      aria-label={t.label}
      className={`box-border flex min-w-0 flex-col gap-2 rounded-[10px] border bg-surface px-3.5 pt-3.5 pb-2.5 ${
        t.over ? 'border-error-border' : 'border-border'
      }`}
    >
      <div className="flex items-center justify-between gap-2">
        <span className="text-xs tracking-[0.06em] text-muted uppercase">{t.label}</span>
        {t.flag && (
          <span className={`rounded-full px-[7px] py-px text-[11.5px] ${FLAG[t.flag.tone]}`}>
            {t.flag.text}
          </span>
        )}
      </div>
      <div className="flex min-h-[30px] flex-wrap items-baseline gap-x-1.5">
        <span className="font-mono text-[22px] font-semibold tracking-[-0.02em] tabular-nums">
          {t.value}
        </span>
        {t.sub && <span className="font-mono text-xs text-muted">{t.sub}</span>}
      </div>
      <Sparkline values={t.values} max={t.max} color={t.color} fill={tileFill(t)} />
    </article>
  )
}

/** TileSkeleton holds one tile's place while the reads are out. */
function TileSkeleton() {
  return (
    <div
      data-skeleton-tile=""
      className="box-border flex flex-col gap-2 rounded-[10px] border border-border bg-surface px-3.5 pt-3.5 pb-2.5"
    >
      <Skeleton className="h-3 w-1/3" />
      <Skeleton className="h-[30px] w-1/2" />
      <Skeleton className="h-8 w-full" />
    </div>
  )
}
