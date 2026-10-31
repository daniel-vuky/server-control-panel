import { useCallback } from 'react'
import { useSearchParams } from 'react-router'
import type { MetricsRange } from '../api/metrics'

/** RangeKey is a chart period, as the metrics routes take it. */
export type RangeKey = MetricsRange

/** RangeInfo is a period's switch label and the start and middle words of its time axis. */
export type RangeInfo = { key: RangeKey; label: string; start: string; mid: string }

/** RANGES are the chart periods in the order the switch lists them (designs 02 and 05). */
export const RANGES: readonly RangeInfo[] = [
  { key: '1h', label: '1 hour', start: '−60 min', mid: '−30 min' },
  { key: '24h', label: '24 hours', start: '−24 h', mid: '−12 h' },
  { key: '7d', label: '7 days', start: '−7 days', mid: '−3.5 days' },
]

export const isRangeKey = (v: string | null): v is RangeKey => RANGES.some((r) => r.key === v)

/** rangeInfo is the period of a key (the first one for a key it does not know). */
export const rangeInfo = (key: RangeKey): RangeInfo =>
  RANGES.find((r) => r.key === key) ?? RANGES[0]

/**
 * useRangeParam keeps a page's chart period in the URL query (`?range=`), so a
 * reload or a shared link shows the same period. A missing or unknown value
 * reads as defaultKey; setting one replaces the history entry and keeps the
 * other parameters.
 */
export function useRangeParam(defaultKey: RangeKey): [RangeKey, (key: RangeKey) => void] {
  const [params, setParams] = useSearchParams()
  const raw = params.get('range')
  const range = isRangeKey(raw) ? raw : defaultKey
  const setRange = useCallback(
    (key: RangeKey) =>
      setParams(
        (prev) => {
          const next = new URLSearchParams(prev)
          next.set('range', key)
          return next
        },
        { replace: true },
      ),
    [setParams],
  )
  return [range, setRange]
}
