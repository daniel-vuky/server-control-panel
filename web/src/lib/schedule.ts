import type { ScheduleFields, TimeZone } from '../api/types.gen'

/** FREQUENCIES are the schedule's choices, in the order the form lists them. */
export const FREQUENCIES = [
  { value: 'off', label: 'Off' },
  { value: 'daily', label: 'Daily' },
  { value: 'weekly', label: 'Weekly' },
] as const

/** WEEKDAYS lists Monday first; the values are Go's time.Weekday (0 = Sunday). */
export const WEEKDAYS = [
  { value: 1, label: 'Monday' },
  { value: 2, label: 'Tuesday' },
  { value: 3, label: 'Wednesday' },
  { value: 4, label: 'Thursday' },
  { value: 5, label: 'Friday' },
  { value: 6, label: 'Saturday' },
  { value: 0, label: 'Sunday' },
] as const

/** ScheduleDraft is a schedule as the form holds it: Copies to keep is the input's text. */
export type ScheduleDraft = Omit<ScheduleFields, 'keep'> & { keep: string }

/** ScheduleField names a schedule field, as a 422 FieldError names it. */
export type ScheduleField = keyof ScheduleDraft

export const SCHEDULE_FIELDS: readonly ScheduleField[] = [
  'frequency',
  'time',
  'weekday',
  'keep',
  'compress',
]

export const isScheduleField = (name: string): name is ScheduleField =>
  (SCHEDULE_FIELDS as readonly string[]).includes(name)

export const draftOf = (f: ScheduleFields): ScheduleDraft => ({
  frequency: f.frequency,
  time: f.time,
  weekday: f.weekday,
  keep: String(f.keep),
  compress: f.compress,
})

/** fieldsOf is the draft to send: a keep that is not a number goes as 0, which the server refuses. */
export function fieldsOf(d: ScheduleDraft): ScheduleFields {
  const keep = d.keep.trim() === '' ? 0 : Number(d.keep)
  return {
    frequency: d.frequency,
    time: d.time,
    weekday: d.weekday,
    keep: Number.isFinite(keep) ? keep : 0,
    compress: d.compress,
  }
}

export const sameDraft = (a: ScheduleDraft, b: ScheduleDraft): boolean =>
  SCHEDULE_FIELDS.every((k) => a[k] === b[k])

/** ZoneParts is an instant read in a zone: the date (YYYY-MM-DD), the day ("Sat Nov 1") and HH:MM. */
export type ZoneParts = { date: string; day: string; time: string }

const OFFSET = /([+-])(\d{2}):?(\d{2})$/

/** offsetMinutes reads "+07:00" (or the end of an RFC 3339 text); Z or nothing is 0. */
function offsetMinutes(text: string): number | null {
  const m = OFFSET.exec(text)
  if (!m) return null
  const mins = Number(m[2]) * 60 + Number(m[3])
  return m[1] === '-' ? -mins : mins
}

const formatters = new Map<string, Intl.DateTimeFormat>()

/** formatter is a cached en-US formatter for zone; it throws RangeError for a name that is not IANA. */
function formatter(zone: string): Intl.DateTimeFormat {
  let f = formatters.get(zone)
  if (!f) {
    f = new Intl.DateTimeFormat('en-US', {
      timeZone: zone,
      weekday: 'short',
      month: 'short',
      day: 'numeric',
      year: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
      hourCycle: 'h23',
    })
    formatters.set(zone, f)
  }
  return f
}

function partsIn(ms: number, zone: string): ZoneParts {
  const p: Record<string, string> = {}
  for (const { type, value } of formatter(zone).formatToParts(ms)) p[type] = value
  const months = [
    'Jan',
    'Feb',
    'Mar',
    'Apr',
    'May',
    'Jun',
    'Jul',
    'Aug',
    'Sep',
    'Oct',
    'Nov',
    'Dec',
  ]
  const month = String(months.indexOf(p.month) + 1).padStart(2, '0')
  return {
    date: `${p.year}-${month}-${p.day.padStart(2, '0')}`,
    day: `${p.weekday} ${p.month} ${p.day}`,
    time: `${p.hour}:${p.minute}`,
  }
}

/**
 * zoneParts reads an instant in the server zone, never the browser's. When
 * the zone's name is not one Intl knows, it falls back to the offset written
 * in the RFC 3339 text (or the zone's offset now, for a UTC text). Null for
 * a text that is not a time.
 */
export function zoneParts(iso: string, zone: TimeZone): ZoneParts | null {
  const ms = Date.parse(iso)
  if (Number.isNaN(ms)) return null
  try {
    return partsIn(ms, zone.name)
  } catch {
    const off = offsetMinutes(iso) ?? offsetMinutes(zone.offset) ?? 0
    return partsIn(ms + off * 60_000, 'UTC')
  }
}

/** formatInZone is "Sat Nov 1, 02:00" in the server zone; a text that is not a time is shown as it is. */
export function formatInZone(iso: string, zone: TimeZone): string {
  const p = zoneParts(iso, zone)
  return p ? `${p.day}, ${p.time}` : iso
}

/** zoneLabel names the server zone and its offset now: "Asia/Ho_Chi_Minh, UTC+07:00" ("UTC" alone for UTC). */
export function zoneLabel(zone: TimeZone): string {
  if (zone.name === 'UTC' && (offsetMinutes(zone.offset) ?? 0) === 0) return 'UTC'
  return `${zone.name}, UTC${zone.offset}`
}
