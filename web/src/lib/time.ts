const time = new Intl.DateTimeFormat('en-US', {
  hour: '2-digit',
  minute: '2-digit',
  hourCycle: 'h23',
})
const day = new Intl.DateTimeFormat('en-US', { day: 'numeric', month: 'short' })

const part = (f: Intl.DateTimeFormat, d: Date, type: Intl.DateTimeFormatPartTypes) =>
  f.formatToParts(d).find((p) => p.type === type)?.value ?? ''

const sameDay = (a: Date, b: Date) =>
  a.getFullYear() === b.getFullYear() &&
  a.getMonth() === b.getMonth() &&
  a.getDate() === b.getDate()

/**
 * formatLastScan renders an RFC 3339 time in local time: "HH:mm" for today,
 * else "d MMM HH:mm". An empty or invalid time is "never".
 */
export function formatLastScan(iso: string, now: Date = new Date()): string {
  const d = new Date(iso)
  if (iso === '' || Number.isNaN(d.getTime())) return 'never'
  const hhmm = `${part(time, d, 'hour')}:${part(time, d, 'minute')}`
  if (sameDay(d, now)) return hhmm
  return `${part(day, d, 'day')} ${part(day, d, 'month')} ${hhmm}`
}

/** folderMissing is the Not found line of a project whose folder a rescan could not find. */
export function folderMissing(missingSince: string): string {
  const since =
    missingSince === ''
      ? 'Folder missing since the last scan.'
      : `Folder missing since the ${formatLastScan(missingSince)} scan.`
  return `${since} Not removed automatically.`
}
