import type { Project } from '../../api/types.gen'
import { isInFlight } from '../../lib/status'

/** StatusFilter is a chip of the Dashboard's filter bar: All or one settled status. */
export type StatusFilter = 'all' | 'running' | 'partial' | 'stopped' | 'error'

/** View is how the Dashboard lists its projects. */
export type View = 'cards' | 'table'

/** Filters is what the URL query holds: `status`, `q` and `view`. */
export type Filters = { status: StatusFilter; q: string; view: View }

/** STATUS_FILTERS is the chips in order. */
export const STATUS_FILTERS: readonly StatusFilter[] = [
  'all',
  'running',
  'partial',
  'stopped',
  'error',
]

const SETTLED = ['running', 'partial', 'stopped', 'error'] as const

const VIEW_KEY = 'dashboard.view'

const isStatusFilter = (s: string | null): s is StatusFilter =>
  s !== null && (STATUS_FILTERS as readonly string[]).includes(s)

const isView = (s: string | null): s is View => s === 'cards' || s === 'table'

/**
 * filtersOf reads the filters from the URL query. An unknown status is All and
 * an unknown or missing view falls back to the stored one, else cards.
 */
export function filtersOf(params: URLSearchParams, storedView: View | null): Filters {
  const status = params.get('status')
  const view = params.get('view')
  return {
    status: isStatusFilter(status) ? status : 'all',
    q: params.get('q') ?? '',
    view: isView(view) ? view : (storedView ?? 'cards'),
  }
}

/** readStoredView is the last chosen view, or null when none is stored or storage is blocked. */
export function readStoredView(): View | null {
  try {
    const v = localStorage.getItem(VIEW_KEY)
    return isView(v) ? v : null
  } catch {
    return null
  }
}

/** storeView remembers the chosen view; a blocked storage is ignored. */
export function storeView(v: View): void {
  try {
    localStorage.setItem(VIEW_KEY, v)
  } catch {
    // Private window or blocked site data: the view just is not remembered.
  }
}

/**
 * matches reports whether a row shows under the filters: its status must be the
 * chip's (an in-flight row stays under every chip, Unknown and Not found rows
 * only under All) and the display name must contain the trimmed search, as
 * plain text and ignoring case.
 */
export function matches(p: Project, f: Filters): boolean {
  if (f.status !== 'all' && !isInFlight(p.status) && p.status !== f.status) return false
  const q = f.q.trim().toLowerCase()
  return q === '' || p.displayName.toLowerCase().includes(q)
}

/** countByStatus counts the settled statuses; `all` counts every row. */
export function countByStatus(rows: Project[]): Record<StatusFilter, number> {
  const counts: Record<StatusFilter, number> = {
    all: rows.length,
    running: 0,
    partial: 0,
    stopped: 0,
    error: 0,
  }
  for (const r of rows) {
    if ((SETTLED as readonly string[]).includes(r.status))
      counts[r.status as (typeof SETTLED)[number]]++
  }
  return counts
}
