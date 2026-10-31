const MB = 1024 * 1024

/** formatMem is bytes as "640 MB" under 1,024 MB, else "1.2 GB"; null is "—". */
export function formatMem(bytes: number | null): string {
  if (bytes === null) return '—'
  const mb = bytes / MB
  if (Math.round(mb) < 1024) return `${Math.round(mb)} MB`
  return `${(mb / 1024).toFixed(1)} GB`
}

/**
 * formatPercent is a percentage rounded to a whole number ("34%"), or to
 * `digits` decimals ("9.8%"); null is "—".
 */
export function formatPercent(p: number | null, digits = 0): string {
  if (p === null) return '—'
  return digits === 0 ? `${Math.round(p)}%` : `${p.toFixed(digits)}%`
}

/** formatCpu is a CPU percentage, as formatPercent words it. */
export function formatCpu(percent: number | null, digits = 0): string {
  return formatPercent(percent, digits)
}

/**
 * formatUptime is how long ago an RFC 3339 time was: "45m" under an hour,
 * "5h 12m" under a day, else "12d 4h". An empty or invalid time is "—".
 */
export function formatUptime(since: string, now: Date = new Date()): string {
  const at = Date.parse(since)
  if (since === '' || Number.isNaN(at)) return '—'
  const minutes = Math.max(0, Math.floor((now.getTime() - at) / 60_000))
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ${minutes % 60}m`
  return `${Math.floor(hours / 24)}d ${hours % 24}h`
}

const BYTE_UNITS = ['KB', 'MB', 'GB', 'TB', 'PB']

/**
 * formatBytes is a size as the server words it (1024-based): "512 B",
 * "4.2 GB", "38 MB"; one decimal below 10, else none.
 */
export function formatBytes(n: number): string {
  if (n < 1024) return `${Math.max(0, Math.round(n))} B`
  let size = 1024
  let unit = BYTE_UNITS[0]
  for (let i = 0; i < BYTE_UNITS.length; i++) {
    unit = BYTE_UNITS[i]
    if (n < size * 1024 || i === BYTE_UNITS.length - 1) break
    size *= 1024
  }
  const v = n / size
  return `${v < 10 ? v.toFixed(1) : v.toFixed(0)} ${unit}`
}

/**
 * formatDuration is how long a backup took: "41s", "1m 02s", "1h 05m"
 * (design 02). Zero or less is "—" (a backup that never ran).
 */
export function formatDuration(ms: number): string {
  if (!(ms > 0)) return '—'
  const s = Math.max(1, Math.round(ms / 1000))
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ${String(s % 60).padStart(2, '0')}s`
  return `${Math.floor(m / 60)}h ${String(m % 60).padStart(2, '0')}m`
}

/** formatRate is a rate in bytes per second as formatBytes words it, plus "/s"; null is "—". */
export function formatRate(bytesPerSec: number | null): string {
  return bytesPerSec === null ? '—' : `${formatBytes(bytesPerSec)}/s`
}

const NICE = [1, 2, 4, 5, 6, 8, 10]

/**
 * niceMax is the top of a chart's scale: the smallest round number at or
 * above v whose half reads well too (1, 2, 4, 5, 6, 8 or 10 times a power of
 * ten). With bytes the rounding is done in the largest 1024-based unit the
 * value reaches (KB, MB, GB…, as formatBytes words them), so 3.1 MB tops out
 * at 4 MB, and a result of 1024 units or more is one of the next unit.
 * Without, it is decimal (CPU in Docker's % can pass 1,000 on a many-core
 * host). Anything at or below 1 (and a value that is not a number) is 1.
 */
export function niceMax(v: number, bytes = false): number {
  if (!(v > 1)) return 1
  let unit = 1
  if (bytes) while (v / unit >= 1024) unit *= 1024
  const m = v / unit
  const p = 10 ** Math.floor(Math.log10(m))
  // A small tolerance keeps 100 at 100 when the division lands just above it.
  const f = m / p - 1e-9
  const nice = (NICE.find((n) => n >= f) ?? 10) * p
  return bytes && nice >= 1024 ? unit * 1024 : nice * unit
}
