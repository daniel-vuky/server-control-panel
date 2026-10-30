import { describe, expect, it } from 'vitest'
import { detectLevel, LEVEL_COLOR } from './logLevel'

describe('detectLevel', () => {
  const cases: Array<[string, ReturnType<typeof detectLevel>]> = [
    ['ERROR payment webhook signature mismatch', 'ERROR'],
    ['err: connection refused', 'ERROR'],
    ['[error] 29#29: upstream timed out', 'ERROR'],
    ['2026/10/30 10:41:12 [error] 29#29: *1 connect() failed', 'ERROR'],
    ['2026-10-30 10:41:12.123 UTC [1] ERROR:  relation does not exist', 'ERROR'],
    ['WARN rate limit near threshold', 'WARN'],
    ['Warning: deprecated option', 'WARN'],
    ['[WARNING] disk almost full', 'WARN'],
    ['INFO GET /v1/products 200 15ms', 'INFO'],
    ['2026-10-30T10:41:12.123Z info: listening on :3000', 'INFO'],
    ['debug redis SET session ttl=3600', 'DEBUG'],
    ['TRACE entering handler', 'TRACE'],
    ['FATAL out of memory', 'FATAL'],
    ['time="2026-10-30" level=warning msg="slow"', 'WARN'],
    // JSON lines: the level field, by name or by pino's number.
    ['{"level":"error","msg":"boom"}', 'ERROR'],
    ['{"level":"Warning","msg":"x"}', 'WARN'],
    ['{"level":30,"msg":"hello"}', 'INFO'],
    ['{"level":60,"msg":"dead"}', 'FATAL'],
    ['{"msg":"no level here"}', null],
    // Not a level word: part of another word, a path, or too far in.
    ['GET /info 200 1ms', null],
    ['information about errors', null],
    ['Errors: 0', null],
    ['GET /v1/products?page=2 200 17ms                ERROR', null],
    ['plain line with nothing', null],
    ['', null],
  ]

  it.each(cases)('%j → %s', (text, want) => {
    expect(detectLevel(text)).toBe(want)
  })

  it('colours each level per design 02', () => {
    expect(LEVEL_COLOR).toEqual({
      INFO: '#7FB8FF',
      WARN: '#F2CF6B',
      ERROR: '#FF8C8F',
      FATAL: '#FF8C8F',
      DEBUG: '#949CA8',
      TRACE: '#949CA8',
    })
  })
})
