import { describe, expect, it } from 'vitest'
import {
  formatBytes,
  formatCpu,
  formatDuration,
  formatMem,
  formatPercent,
  formatRate,
  formatUptime,
  niceMax,
} from './format'

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

  it('keeps the decimals it is asked for', () => {
    expect(formatCpu(9.84, 1)).toBe('9.8%')
    expect(formatCpu(20, 1)).toBe('20.0%')
    expect(formatCpu(null, 1)).toBe('—')
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

describe('formatBytes', () => {
  it.each([
    [0, '0 B'],
    [512, '512 B'],
    [1024, '1.0 KB'],
    [38 * MB, '38 MB'],
    [4.2 * 1024 * MB, '4.2 GB'],
    [212 * MB, '212 MB'],
  ])('%s is %s, as the server words it', (n, want) => {
    expect(formatBytes(n)).toBe(want)
  })
})

describe('formatDuration', () => {
  it.each([
    [0, '—'],
    [-5, '—'],
    [400, '1s'],
    [41_000, '41s'],
    [62_000, '1m 02s'],
    [59 * 60_000 + 59_400, '59m 59s'],
    [3_900_000, '1h 05m'],
  ])('%s ms is %s', (ms, want) => {
    expect(formatDuration(ms)).toBe(want)
  })
})

describe('formatRate', () => {
  it.each([
    [null, '—'],
    [0, '0 B/s'],
    [512, '512 B/s'],
    [3.1 * MB, '3.1 MB/s'],
    [38 * MB, '38 MB/s'],
  ])('%s is %s', (n, want) => {
    expect(formatRate(n)).toBe(want)
  })
})

describe('formatPercent', () => {
  it.each([
    [null, 0, '—'],
    [34, 0, '34%'],
    [64.6, 0, '65%'],
    [9.84, 1, '9.8%'],
  ])('%s with %s digits is %s', (p, digits, want) => {
    expect(formatPercent(p, digits)).toBe(want)
  })
})

describe('niceMax', () => {
  it.each([
    [0, 2],
    [0.3, 2],
    [1, 2],
    [1.5, 2],
    [2, 2],
    [Number.NaN, 2],
    [2.2, 4],
    [7, 8],
    [34, 40],
    [90, 100],
    [100, 100],
    [130, 200],
    [600, 600],
    [1010, 2000],
    [1100, 2000],
    [3.1 * MB, 4_000_000],
  ])('%s rounds up to %s', (v, want) => {
    expect(niceMax(v)).toBe(want)
  })

  const GB = 1024 * MB
  it.each([
    [0, 2],
    [1, 2],
    [600, 600],
    [1010, 1024],
    [1500, 2048],
    [3.1 * MB, 4 * MB],
    [640 * MB, 800 * MB],
    [1010 * MB, GB],
    [7.7 * GB, 8 * GB],
    [8 * GB, 8 * GB],
  ])('%s bytes round up to %s', (v, want) => {
    expect(niceMax(v, true)).toBe(want)
  })
})
