import type { TimeZone } from '../../api/types.gen'
import { zoneParts } from '../../lib/schedule'

/** Kind is the event type a filter keeps. */
export type Kind = 'action' | 'backup' | 'alert'

/** KINDS are the chips after All, in order. */
export const KINDS: [Kind, string][] = [
  ['action', 'Actions'],
  ['backup', 'Backups'],
  ['alert', 'Alerts'],
]

/** parseFilters reads the filters from a URL query; a value that is not valid is left out. */
export function parseFilters(search: URLSearchParams): { kind?: Kind; project?: number } {
  const out: { kind?: Kind; project?: number } = {}
  const kind = search.get('kind')
  if (kind === 'action' || kind === 'backup' || kind === 'alert') out.kind = kind
  const text = search.get('project') ?? ''
  const project = Number(text)
  if (/^\d+$/.test(text) && Number.isSafeInteger(project) && project > 0) out.project = project
  return out
}

const DATE = /^(\d{4})-(\d{2})-(\d{2})/
const WEEKDAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']

/** dateOf is the YYYY-MM-DD of an RFC 3339 text as written, in the zone it carries; '' for anything else. */
export function dateOf(at: string): string {
  const m = DATE.exec(at)
  return m ? `${m[1]}-${m[2]}-${m[3]}` : ''
}

/** clockOf is the HH:MM of an RFC 3339 text as written, in the zone it carries; the text itself for anything else. */
export function clockOf(at: string): string {
  const m = /^\d{4}-\d{2}-\d{2}T(\d{2}:\d{2})/.exec(at)
  return m ? m[1] : at
}

const utc = (date: string) => {
  const [y, m, d] = date.split('-').map(Number)
  return new Date(Date.UTC(y, m - 1, d))
}

/**
 * dayLabel is a day's heading: "Today · 01/11", "Yesterday · 31/10", else
 * "Thu 30/10". `at` is an RFC 3339 text in the server zone and `today` the
 * server-zone date of now (YYYY-MM-DD); the date is split from the text, never
 * read through a browser-zone Date. A text that is not a time is shown as it is.
 */
export function dayLabel(at: string, today: string): string {
  const date = dateOf(at)
  if (date === '') return at
  const dm = `${date.slice(8, 10)}/${date.slice(5, 7)}`
  if (date === today) return `Today · ${dm}`
  const yesterday =
    today === '' ? '' : new Date(utc(today).getTime() - 86_400_000).toISOString().slice(0, 10)
  if (date === yesterday) return `Yesterday · ${dm}`
  return `${WEEKDAYS[utc(date).getUTCDay()]} ${dm}`
}

/**
 * serverToday is the server-zone date of now (YYYY-MM-DD): in the zone the
 * server reported, else in the offset the newest record carries, else the
 * browser's own.
 */
export function serverToday(now: Date, zone: TimeZone | null, newestAt?: string): string {
  const iso = now.toISOString()
  if (zone) {
    const p = zoneParts(iso, zone)
    if (p) return p.date
  }
  const offset = newestAt === undefined ? undefined : /([+-]\d{2}:\d{2})$/.exec(newestAt)?.[1]
  if (offset) {
    const p = zoneParts(iso, { name: '', offset })
    if (p) return p.date
  }
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`
}
