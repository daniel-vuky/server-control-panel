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

/** clock is "HH:MM" of a valid date, in local time. */
const clock = (d: Date) => `${part(time, d, 'hour')}:${part(time, d, 'minute')}`

const parse = (iso: string): Date | null => {
  const d = new Date(iso)
  return iso === '' || Number.isNaN(d.getTime()) ? null : d
}

/**
 * formatWhen is a backup's time as design 02 shows it, in local time:
 * "Oct 28 02:00", with the year when it is not this year. An empty or invalid
 * time is "—".
 */
export function formatWhen(iso: string, now: Date = new Date()): string {
  const d = parse(iso)
  if (d === null) return '—'
  const date = `${part(day, d, 'month')} ${part(day, d, 'day')}`
  const year = d.getFullYear() === now.getFullYear() ? '' : ` ${d.getFullYear()}`
  return `${date}${year} ${clock(d)}`
}

/**
 * formatRecent is formatWhen with the near days in words, for the Dashboard's
 * latest backup: "02:00 today", "Yesterday 02:00", else formatWhen's date. An
 * empty or invalid time is "—".
 */
export function formatRecent(iso: string, now: Date = new Date()): string {
  const d = parse(iso)
  if (d === null) return '—'
  if (sameDay(d, now)) return `${clock(d)} today`
  const yesterday = new Date(now.getFullYear(), now.getMonth(), now.getDate() - 1)
  if (sameDay(d, yesterday)) return `Yesterday ${clock(d)}`
  return formatWhen(iso, now)
}

/** formatClock is "HH:MM" of a time in local time; an empty or invalid time is "—". */
export function formatClock(iso: string): string {
  const d = parse(iso)
  return d === null ? '—' : clock(d)
}

/** folderMissing is the Not found line of a project whose folder a rescan could not find. */
export function folderMissing(missingSince: string): string {
  const since =
    missingSince === ''
      ? 'Folder missing since the last scan.'
      : `Folder missing since the ${formatLastScan(missingSince)} scan.`
  return `${since} Not removed automatically.`
}
