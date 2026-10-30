import { describe, expect, it } from 'vitest'
import { formatCpu, formatMem, formatUptime } from './format'

const MB = 1024 * 1024

describe('formatMem', () => {
  it.each([
    [null, '—'],
    [0, '0 MB'],
    [640 * MB, '640 MB'],
    [1023 * MB, '1023 MB'],
    [1024 * MB, '1.0 GB'],
    [1.2 * 1024 * MB, '1.2 GB'],
    [4 * 1024 * MB, '4.0 GB'],
  ])('%s is %s', (bytes, want) => {
    expect(formatMem(bytes)).toBe(want)
  })
})

describe('formatCpu', () => {
  it.each([
    [null, '—'],
    [0, '0%'],
    [12, '12%'],
    [12.4, '12%'],
    [12.6, '13%'],
    [250, '250%'],
  ])('%s is %s', (p, want) => {
    expect(formatCpu(p)).toBe(want)
  })
})

describe('formatUptime', () => {
  const now = new Date('2026-10-30T12:00:00Z')
  const ago = (ms: number) => new Date(now.getTime() - ms).toISOString()
  const min = 60_000
  const hour = 60 * min
  const day = 24 * hour

  it.each([
    ['', '—'],
    ['not a time', '—'],
    [ago(0), '0m'],
    [ago(45 * min), '45m'],
    [ago(5 * hour + 12 * min), '5h 12m'],
    [ago(12 * day + 4 * hour), '12d 4h'],
    [ago(-5 * min), '0m'],
  ])('%s is %s', (since, want) => {
    expect(formatUptime(since, now)).toBe(want)
  })
})
