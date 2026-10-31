import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useVisibleInterval } from './useVisibleInterval'

let visibility: DocumentVisibilityState = 'visible'

function setVisibility(v: DocumentVisibilityState) {
  visibility = v
  act(() => {
    document.dispatchEvent(new Event('visibilitychange'))
  })
}

describe('useVisibleInterval', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    visibility = 'visible'
    Object.defineProperty(document, 'visibilityState', {
      configurable: true,
      get: () => visibility,
    })
  })

  afterEach(() => {
    vi.useRealTimers()
    // Drop the own property so the prototype's getter is back.
    Reflect.deleteProperty(document, 'visibilityState')
  })

  it('calls the function every period while the page is visible', () => {
    const fn = vi.fn()
    renderHook(() => useVisibleInterval(fn, 30_000))
    expect(fn).not.toHaveBeenCalled()
    act(() => vi.advanceTimersByTime(30_000))
    expect(fn).toHaveBeenCalledTimes(1)
    act(() => vi.advanceTimersByTime(60_000))
    expect(fn).toHaveBeenCalledTimes(3)
  })

  it('pauses while hidden and calls once more on becoming visible', () => {
    const fn = vi.fn()
    renderHook(() => useVisibleInterval(fn, 30_000))
    setVisibility('hidden')
    act(() => vi.advanceTimersByTime(120_000))
    expect(fn).not.toHaveBeenCalled()

    setVisibility('visible')
    expect(fn).toHaveBeenCalledTimes(1)
    // The period restarts from the moment it became visible.
    act(() => vi.advanceTimersByTime(29_000))
    expect(fn).toHaveBeenCalledTimes(1)
    act(() => vi.advanceTimersByTime(1_000))
    expect(fn).toHaveBeenCalledTimes(2)
  })

  it('does not start while the page is hidden at mount', () => {
    visibility = 'hidden'
    const fn = vi.fn()
    renderHook(() => useVisibleInterval(fn, 30_000))
    act(() => vi.advanceTimersByTime(90_000))
    expect(fn).not.toHaveBeenCalled()
  })

  it('calls the latest function and stops at unmount', () => {
    const first = vi.fn()
    const second = vi.fn()
    const { rerender, unmount } = renderHook(({ fn }) => useVisibleInterval(fn, 30_000), {
      initialProps: { fn: first },
    })
    rerender({ fn: second })
    act(() => vi.advanceTimersByTime(30_000))
    expect(first).not.toHaveBeenCalled()
    expect(second).toHaveBeenCalledTimes(1)

    unmount()
    act(() => vi.advanceTimersByTime(60_000))
    setVisibility('hidden')
    setVisibility('visible')
    expect(second).toHaveBeenCalledTimes(1)
  })
})
