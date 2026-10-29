import { act, renderHook } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { useLoad } from './useLoad'

type Deferred = {
  signal: AbortSignal
  resolve(v: string): void
  reject(e: Error): void
}

/** harness makes a fetcher whose every call is a promise the test settles. */
function harness() {
  const calls: Deferred[] = []
  const fetcher = (signal: AbortSignal) =>
    new Promise<string>((resolve, reject) => {
      calls.push({ signal, resolve, reject })
    })
  return { calls, fetcher }
}

describe('useLoad', () => {
  it('goes from loading to ok', async () => {
    const h = harness()
    const { result } = renderHook(() => useLoad(h.fetcher))
    expect(result.current.load).toEqual({ state: 'loading' })
    await act(async () => h.calls[0].resolve('data'))
    expect(result.current.load).toEqual({ state: 'ok', data: 'data' })
  })

  it('reports an error message', async () => {
    const h = harness()
    const { result } = renderHook(() => useLoad(h.fetcher))
    await act(async () => h.calls[0].reject(new Error('boom')))
    expect(result.current.load).toEqual({ state: 'error', message: 'boom' })
  })

  it('does not refetch when the fetcher is a new inline function every render', async () => {
    const h = harness()
    const { result, rerender } = renderHook(() => useLoad((s) => h.fetcher(s)))
    rerender()
    rerender()
    expect(h.calls).toHaveLength(1)
    await act(async () => h.calls[0].resolve('data'))
    expect(result.current.load).toEqual({ state: 'ok', data: 'data' })
    expect(h.calls).toHaveLength(1)
  })

  it('aborts the request at unmount and ignores its late answer', async () => {
    const h = harness()
    const { unmount } = renderHook(() => useLoad(h.fetcher))
    expect(h.calls[0].signal.aborted).toBe(false)
    unmount()
    expect(h.calls[0].signal.aborted).toBe(true)
    // Settling after unmount must not throw or warn.
    await act(async () => h.calls[0].resolve('late'))
  })

  it('retry aborts the old request, and a stale answer does not overwrite a newer one', async () => {
    const h = harness()
    const { result } = renderHook(() => useLoad(h.fetcher))
    act(() => result.current.retry())
    expect(h.calls).toHaveLength(2)
    expect(h.calls[0].signal.aborted).toBe(true)
    expect(h.calls[1].signal.aborted).toBe(false)

    await act(async () => h.calls[1].resolve('new'))
    expect(result.current.load).toEqual({ state: 'ok', data: 'new' })
    await act(async () => h.calls[0].resolve('stale'))
    expect(result.current.load).toEqual({ state: 'ok', data: 'new' })
    await act(async () => h.calls[0].reject(new Error('stale error')))
    expect(result.current.load).toEqual({ state: 'ok', data: 'new' })
  })
})
