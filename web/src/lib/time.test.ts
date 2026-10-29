import { describe, expect, it } from 'vitest'
import { formatLastScan } from './time'

describe('formatLastScan', () => {
  const now = new Date(2026, 9, 29, 15, 0) // 29 Oct 2026, local time

  it('shows HH:mm for a time today', () => {
    expect(formatLastScan(new Date(2026, 9, 29, 9, 5).toISOString(), now)).toBe('09:05')
  })

  it('shows midnight as 00:xx, not 24:xx', () => {
    expect(formatLastScan(new Date(2026, 9, 29, 0, 7).toISOString(), now)).toBe('00:07')
  })

  it('shows d MMM HH:mm for another day', () => {
    expect(formatLastScan(new Date(2026, 8, 3, 18, 30).toISOString(), now)).toBe('3 Sep 18:30')
  })

  it('treats the same day of another year as another day', () => {
    expect(formatLastScan(new Date(2025, 9, 29, 9, 5).toISOString(), now)).toBe('29 Oct 09:05')
  })

  it('shows "never" for an empty or invalid time', () => {
    expect(formatLastScan('', now)).toBe('never')
    expect(formatLastScan('not a date', now)).toBe('never')
  })
})
