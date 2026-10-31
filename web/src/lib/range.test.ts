import { act, renderHook } from '@testing-library/react'
import { createElement, type ReactNode } from 'react'
import { MemoryRouter, useLocation } from 'react-router'
import { describe, expect, it } from 'vitest'
import { RANGES, useRangeParam } from './range'

function setup(entry: string) {
  const wrapper = ({ children }: { children: ReactNode }) =>
    createElement(MemoryRouter, { initialEntries: [entry] }, children)
  return renderHook(() => ({ range: useRangeParam('24h'), location: useLocation() }), { wrapper })
}

describe('RANGES', () => {
  it('lists the three periods in order with their axis words', () => {
    expect(RANGES.map((r) => [r.key, r.label, r.start, r.mid])).toEqual([
      ['1h', '1 hour', '−60 min', '−30 min'],
      ['24h', '24 hours', '−24 h', '−12 h'],
      ['7d', '7 days', '−7 days', '−3.5 days'],
    ])
  })
})

describe('useRangeParam', () => {
  it('reads the range from the URL', () => {
    const { result } = setup('/server?range=7d')
    expect(result.current.range[0]).toBe('7d')
  })

  it('falls back to the default without a range or for a bad one', () => {
    expect(setup('/server').result.current.range[0]).toBe('24h')
    expect(setup('/server?range=2y').result.current.range[0]).toBe('24h')
  })

  it('writes the range into the URL and keeps the other parameters', () => {
    const { result } = setup('/server?tab=x')
    act(() => result.current.range[1]('1h'))
    expect(result.current.range[0]).toBe('1h')
    const params = new URLSearchParams(result.current.location.search)
    expect(params.get('range')).toBe('1h')
    expect(params.get('tab')).toBe('x')
  })
})
