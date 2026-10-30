const MB = 1024 * 1024

/** formatMem is bytes as "640 MB" under 1,024 MB, else "1.2 GB"; null is "—". */
export function formatMem(bytes: number | null): string {
  if (bytes === null) return '—'
  const mb = bytes / MB
  if (Math.round(mb) < 1024) return `${Math.round(mb)} MB`
  return `${(mb / 1024).toFixed(1)} GB`
}

/**
 * formatCpu is a CPU percentage rounded to a whole number ("12%"), or to
 * `digits` decimals ("9.8%"); null is "—".
 */
export function formatCpu(percent: number | null, digits = 0): string {
  if (percent === null) return '—'
  return digits === 0 ? `${Math.round(percent)}%` : `${percent.toFixed(digits)}%`
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
