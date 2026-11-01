import { ApiError, getJSON } from './client'
import type { ActivityPage } from './types.gen'

/** ActivityQuery filters GET /api/activity; before is the last id shown, limit is 1 to 100. */
export type ActivityQuery = {
  kind?: string
  project?: number
  before?: number
  limit?: number
}

/**
 * getActivity is one page of the activity record, newest first: the newest
 * page, or the one before activity id `before`.
 */
export async function getActivity(
  q: ActivityQuery = {},
  signal?: AbortSignal,
): Promise<ActivityPage> {
  const params = new URLSearchParams()
  for (const key of ['kind', 'project', 'before', 'limit'] as const) {
    const v = q[key]
    if (v !== undefined && v !== '') params.set(key, String(v))
  }
  const qs = params.toString()
  const page = await getJSON<ActivityPage>(
    qs === '' ? '/api/activity' : `/api/activity?${qs}`,
    signal,
  )
  if (typeof page !== 'object' || page === null || !Array.isArray(page.entries)) {
    throw new ApiError(200, null, 'Unexpected response from the server.')
  }
  return { entries: page.entries, more: page.more === true }
}
