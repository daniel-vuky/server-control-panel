import { describe, expect, it } from 'vitest'
import {
  draftOf,
  fieldsOf,
  formatInZone,
  sameDraft,
  WEEKDAYS,
  zoneLabel,
  zoneParts,
} from './schedule'

const hcm = { name: 'Asia/Ho_Chi_Minh', offset: '+07:00' }

describe('WEEKDAYS', () => {
  it('lists Monday first with Go weekday values (0 = Sunday)', () => {
    expect(WEEKDAYS.map((d) => d.value)).toEqual([1, 2, 3, 4, 5, 6, 0])
    expect(WEEKDAYS[0].label).toBe('Monday')
    expect(WEEKDAYS[6].label).toBe('Sunday')
  })
})

describe('formatInZone', () => {
  it('formats in the server zone, not the browser zone', () => {
    // 19:00 UTC on Saturday 31 Oct is 02:00 on Sunday 1 Nov in Ho Chi Minh City.
    expect(formatInZone('2026-10-31T19:00:00Z', hcm)).toBe('Sun Nov 1, 02:00')
    expect(formatInZone('2026-11-01T02:00:00+07:00', hcm)).toBe('Sun Nov 1, 02:00')
    expect(
      formatInZone('2026-10-31T19:00:00Z', { name: 'America/New_York', offset: '-04:00' }),
    ).toBe('Sat Oct 31, 15:00')
  })

  it('follows the zone rules on the day itself (DST), not the offset of today', () => {
    // New York leaves DST on 1 Nov 2026: on 2 Nov the zone is UTC-05:00.
    expect(
      formatInZone('2026-11-02T07:00:00Z', { name: 'America/New_York', offset: '-04:00' }),
    ).toBe('Mon Nov 2, 02:00')
  })

  it('falls back to the offset in the ISO text when the name is not an IANA zone', () => {
    expect(formatInZone('2026-11-01T02:00:00+07:00', { name: 'Local', offset: '+07:00' })).toBe(
      'Sun Nov 1, 02:00',
    )
    // A UTC instant falls back to the zone's offset.
    expect(formatInZone('2026-10-31T19:00:00Z', { name: 'Local', offset: '+07:00' })).toBe(
      'Sun Nov 1, 02:00',
    )
  })

  it('gives the parts for callers that word the day themselves', () => {
    expect(zoneParts('2026-10-31T19:00:00Z', hcm)).toEqual({
      date: '2026-11-01',
      day: 'Sun Nov 1',
      time: '02:00',
    })
  })

  it('answers the text as it is for a value that is not a time', () => {
    expect(formatInZone('soon', hcm)).toBe('soon')
  })
})

describe('zoneLabel', () => {
  it('names the zone and its offset', () => {
    expect(zoneLabel(hcm)).toBe('Asia/Ho_Chi_Minh, UTC+07:00')
    expect(zoneLabel({ name: 'America/New_York', offset: '-04:00' })).toBe(
      'America/New_York, UTC-04:00',
    )
  })

  it('says UTC once for the UTC zone', () => {
    expect(zoneLabel({ name: 'UTC', offset: '+00:00' })).toBe('UTC')
  })
})

describe('drafts', () => {
  const fields = { frequency: 'weekly', time: '02:00', weekday: 3, keep: 14, compress: true }

  it('round-trips the fields through the draft', () => {
    const d = draftOf(fields)
    expect(d).toEqual({ ...fields, keep: '14' })
    expect(fieldsOf(d)).toEqual(fields)
    expect(sameDraft(d, draftOf(fields))).toBe(true)
    expect(sameDraft(d, { ...d, keep: '15' })).toBe(false)
  })

  it('sends a keep that is not a number as 0, for the server to refuse', () => {
    expect(fieldsOf({ ...draftOf(fields), keep: '' }).keep).toBe(0)
    expect(fieldsOf({ ...draftOf(fields), keep: 'abc' }).keep).toBe(0)
    expect(fieldsOf({ ...draftOf(fields), keep: '1.5' }).keep).toBe(1.5)
  })
})
