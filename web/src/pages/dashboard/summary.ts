import type { ServerInfo, ServerPoint, ServerSeries } from '../../api/types.gen'
import { formatPercent, formatSince, formatUsedOfTotal } from '../../lib/format'

/** Tile colours (spec decision D8). */
const ACCENT = '#5AA7FF'
const ORANGE = '#F0913A'
const SUCCESS = '#3FCF8E'
const DANGER = '#FF6B6E'

/** Tile is one of the six server summary tiles, worded and ready to draw. */
export type Tile = {
  key: 'cpu' | 'ram' | 'disk' | 'load' | 'uptime' | 'tunnel'
  label: string
  value: string
  sub: string
  flag: { text: string; tone: 'ok' | 'bad' } | null
  over: boolean
  values: (number | null)[]
  max: number
  color: string
}

/** tileFill is the area fill under a tile's line: design 01's tints, red when the tile is over. */
export function tileFill(t: Tile): string {
  if (t.over) return 'rgba(242,85,90,0.14)'
  if (t.color === SUCCESS) return 'rgba(63,207,142,0.10)'
  if (t.color === ORANGE) return 'rgba(240,145,58,0.12)'
  return 'rgba(90,167,255,0.12)'
}

/** percent is used as a share of total (computed as used*100/total, so a round ratio stays exact), null with no total. */
const percent = (used: number | null, total: number | null) =>
  used === null || total === null || !(total > 0) ? null : (used * 100) / total

const OVER_FLAG = { text: 'Over threshold', tone: 'bad' } as const

/**
 * summaryTiles words the six tiles of the Dashboard's server summary (spec
 * decisions D4 to D8). Values come from the series' newest sample of the last
 * 90 s (null is "—"), the charts from its points, the thresholds, load
 * averages, boot time and tunnel from the facts. A tile is over when its value
 * is above its threshold (the disk tile when any disk is). Either read may be
 * missing: its tiles read "—". Without the host's /proc the first five tiles
 * read "—" and "Not available" and the tunnel keeps its state.
 */
export function summaryTiles(
  info: ServerInfo | null,
  series: ServerSeries | null,
  now: Date,
): Tile[] {
  const latest = series?.latest ?? null
  const points: ServerPoint[] = series?.points ?? []
  const th = info?.thresholds
  const cores = info && info.cores > 0 ? info.cores : 0

  const tile = (t: Partial<Tile> & Pick<Tile, 'key' | 'label' | 'color'>): Tile => {
    const base: Tile = {
      value: '—',
      sub: '',
      flag: null,
      over: false,
      values: [],
      max: 100,
      ...t,
    }
    return base.over ? { ...base, color: DANGER, flag: base.flag ?? OVER_FLAG } : base
  }

  const tunnel = tunnelTile(info, points)
  if (info && !info.available) {
    const na = (key: Tile['key'], label: string, color: string): Tile =>
      tile({ key, label, color, sub: 'Not available' })
    return [
      na('cpu', 'CPU', ACCENT),
      na('ram', 'RAM', ORANGE),
      na('disk', 'Disk', ACCENT),
      na('load', 'Load avg', ACCENT),
      na('uptime', 'Uptime', SUCCESS),
      tunnel,
    ]
  }

  const cpu = latest?.cpu ?? null
  const ram = percent(latest?.memUsed ?? null, latest?.memTotal ?? null)
  const diskUsed = latest?.diskUsed ?? null
  const diskTotal = latest?.diskTotal ?? null
  const disk = percent(diskUsed, diskTotal)

  // Every disk is judged on its own (as System info does); the fullest one over is named.
  let fullest: { mount: string; share: number } | null = null
  for (const d of info?.disks ?? []) {
    const share = percent(d.usedBytes, d.totalBytes)
    if (share === null || !th || !(share > th.diskPercent)) continue
    if (fullest === null || share > fullest.share) fullest = { mount: d.mount, share }
  }

  const loadMax = Math.max(cores, ...points.map((p) => p.load1 ?? 0))

  return [
    tile({
      key: 'cpu',
      label: 'CPU',
      color: ACCENT,
      value: formatPercent(cpu),
      sub: cores > 0 ? `${cores} vCPU` : '',
      over: cpu !== null && th !== undefined && cpu > th.cpuPercent,
      values: points.map((p) => p.cpu),
    }),
    tile({
      key: 'ram',
      label: 'RAM',
      color: ORANGE,
      ...usedOf(latest?.memUsed ?? null, latest?.memTotal ?? null, ram),
      over: ram !== null && th !== undefined && ram > th.ramPercent,
      values: points.map((p) => percent(p.memUsed, p.memTotal)),
    }),
    tile({
      key: 'disk',
      label: 'Disk',
      color: ACCENT,
      ...usedOf(diskUsed, diskTotal, disk),
      ...(fullest ? { sub: `${fullest.mount} ${Math.round(fullest.share)}%` } : {}),
      over: fullest !== null,
      values: points.map((p) => percent(p.diskUsed, p.diskTotal)),
    }),
    tile({
      key: 'load',
      label: 'Load avg',
      color: ACCENT,
      value: latest?.load1 == null ? '—' : latest.load1.toFixed(2),
      sub: info?.load ? `${info.load.five.toFixed(2)} · ${info.load.fifteen.toFixed(2)}` : '',
      values: points.map((p) => p.load1),
      max: loadMax > 0 ? loadMax : 1,
    }),
    uptimeTile(info, points, now),
    tunnel,
  ]
}

/** usedOf is the value and sub of a used / total tile ("5.2 / 8", "GB · 65%"), dashes with no total. */
function usedOf(used: number | null, total: number | null, share: number | null) {
  if (used === null || total === null || share === null) return { value: '—', sub: '' }
  const { value, unit } = formatUsedOfTotal(used, total)
  return { value, sub: `${unit} · ${Math.round(share)}%` }
}

const HOUR_MS = 3_600_000

/** uptimeTile is drawn from the boot time (no column holds it): hours since boot at each point, null before it. */
function uptimeTile(info: ServerInfo | null, points: ServerPoint[], now: Date): Tile {
  const boot = info?.bootTime ? Date.parse(info.bootTime) : Number.NaN
  const base = { key: 'uptime', label: 'Uptime', color: SUCCESS, flag: null, over: false } as const
  if (Number.isNaN(boot) || !info?.bootTime)
    return { ...base, value: '—', sub: '', values: [], max: 100 }
  const values = points.map((p) => {
    const t = Date.parse(p.t)
    return Number.isNaN(t) || t < boot ? null : (t - boot) / HOUR_MS
  })
  const top = Math.max(0, ...values.map((v) => v ?? 0))
  return { ...base, ...formatSince(info.bootTime, now), values, max: top > 0 ? top : 1 }
}

/** tunnelTile words cloudflared's state (spec decision D6): connected is green, down is red, unknown is neutral. */
function tunnelTile(info: ServerInfo | null, points: ServerPoint[]): Tile {
  const base = {
    key: 'tunnel',
    label: 'CF Tunnel',
    values: points.map((p) => p.tunnel),
    max: 1,
  } as const
  if (!info) return { ...base, value: '—', sub: '', flag: null, over: false, color: ACCENT }
  const { state, hostnames } = info.cloudflared
  const sub = state === 'connected' && hostnames !== null ? `${hostnames} hostnames` : 'cloudflared'
  switch (state) {
    case 'connected':
      return {
        ...base,
        value: 'Connected',
        sub,
        flag: { text: 'Healthy', tone: 'ok' },
        over: false,
        color: SUCCESS,
      }
    case 'disconnected':
    case 'not_running':
      return {
        ...base,
        value: state === 'disconnected' ? 'Disconnected' : 'Not running',
        sub,
        flag: { text: 'Down', tone: 'bad' },
        over: true,
        color: DANGER,
      }
    default:
      return {
        ...base,
        value: 'Unknown',
        sub: 'connection unknown',
        flag: null,
        over: false,
        color: ACCENT,
      }
  }
}
