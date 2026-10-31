import { afterEach, describe, expect, it, vi } from 'vitest'
import { makeProject } from '../../test/projects'
import {
  countByStatus,
  type Filters,
  filtersOf,
  matches,
  readStoredView,
  storeView,
} from './filters'

const base: Filters = { status: 'all', q: '', view: 'cards' }
const params = (s: string) => new URLSearchParams(s)

describe('filtersOf', () => {
  it('reads status, q and view from the URL', () => {
    expect(filtersOf(params('status=running&q=shop&view=table'), null)).toEqual({
      status: 'running',
      q: 'shop',
      view: 'table',
    })
  })

  it('ignores an unknown status or view: All, cards', () => {
    expect(filtersOf(params('status=nope&view=grid'), null)).toEqual(base)
    // in flight and unknown statuses are not chips
    expect(filtersOf(params('status=starting'), null).status).toBe('all')
    expect(filtersOf(params('status=toString'), null).status).toBe('all')
  })

  it('falls back to the stored view when the URL has none or an unknown one', () => {
    expect(filtersOf(params(''), 'table').view).toBe('table')
    expect(filtersOf(params('view=grid'), 'table').view).toBe('table')
    expect(filtersOf(params('view=cards'), 'table').view).toBe('cards')
  })
})

describe('matches', () => {
  const row = (status: string, displayName = 'shop') => makeProject({ status, displayName })

  it('matches a settled row by its own chip and under All', () => {
    expect(matches(row('running'), { ...base, status: 'running' })).toBe(true)
    expect(matches(row('running'), { ...base, status: 'stopped' })).toBe(false)
    expect(matches(row('error'), { ...base, status: 'all' })).toBe(true)
  })

  it('keeps an in-flight row under every chip', () => {
    for (const s of ['starting', 'stopping', 'restarting']) {
      for (const status of ['all', 'running', 'partial', 'stopped', 'error'] as const) {
        expect(matches(row(s), { ...base, status })).toBe(true)
      }
    }
  })

  it('shows unknown and notfound rows under All only', () => {
    for (const s of ['unknown', 'notfound']) {
      expect(matches(row(s), base)).toBe(true)
      for (const status of ['running', 'partial', 'stopped', 'error'] as const) {
        expect(matches(row(s), { ...base, status })).toBe(false)
      }
    }
  })

  it('searches the display name: trimmed, case-insensitive, plain substring', () => {
    expect(matches(row('running', 'Shop API'), { ...base, q: '  shop ' })).toBe(true)
    expect(matches(row('running', 'Shop API'), { ...base, q: 'API' })).toBe(true)
    expect(matches(row('running', 'Shop API'), { ...base, q: 'blog' })).toBe(false)
    expect(matches(row('running', 'Shop API'), { ...base, q: '   ' })).toBe(true)
  })

  it('does not treat the search as a pattern', () => {
    expect(matches(row('running', 'shop (v2)'), { ...base, q: '  shop (' })).toBe(true)
    expect(matches(row('running', 'shop'), { ...base, q: 'sh.p' })).toBe(false)
    expect(matches(row('running', 'a+b'), { ...base, q: '[' })).toBe(false)
    expect(() => matches(row('running'), { ...base, q: '(*[\\' })).not.toThrow()
  })

  it('combines status and search (AND)', () => {
    const f: Filters = { ...base, status: 'running', q: 'shop' }
    expect(matches(row('running', 'shop'), f)).toBe(true)
    expect(matches(row('stopped', 'shop'), f)).toBe(false)
    expect(matches(row('running', 'blog'), f)).toBe(false)
  })
})

describe('countByStatus', () => {
  it('counts the four settled statuses and every row under all', () => {
    const rows = [
      makeProject({ id: 1, status: 'running' }),
      makeProject({ id: 2, status: 'running' }),
      makeProject({ id: 3, status: 'partial' }),
      makeProject({ id: 4, status: 'error' }),
      makeProject({ id: 5, status: 'starting' }),
      makeProject({ id: 6, status: 'notfound' }),
      makeProject({ id: 7, status: 'unknown' }),
    ]
    expect(countByStatus(rows)).toEqual({ all: 7, running: 2, partial: 1, stopped: 0, error: 1 })
  })

  it('is all zeros for no rows', () => {
    expect(countByStatus([])).toEqual({ all: 0, running: 0, partial: 0, stopped: 0, error: 0 })
  })
})

describe('stored view', () => {
  afterEach(() => {
    vi.restoreAllMocks()
    localStorage.clear()
  })

  it('round-trips through dashboard.view', () => {
    expect(readStoredView()).toBeNull()
    storeView('table')
    expect(localStorage.getItem('dashboard.view')).toBe('table')
    expect(readStoredView()).toBe('table')
    storeView('cards')
    expect(readStoredView()).toBe('cards')
  })

  it('ignores a stored value that is not a view', () => {
    localStorage.setItem('dashboard.view', 'grid')
    expect(readStoredView()).toBeNull()
  })

  it('survives a localStorage that throws', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('blocked')
    })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('blocked')
    })
    expect(readStoredView()).toBeNull()
    expect(() => storeView('table')).not.toThrow()
    expect(filtersOf(params(''), readStoredView()).view).toBe('cards')
  })
})
