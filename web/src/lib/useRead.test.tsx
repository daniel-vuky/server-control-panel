import { act, renderHook, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { errorMessage, useRead } from './useRead'

/** deferred is a promise the test settles by hand. */
function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

describe('useRead', () => {
  it('loads at mount', async () => {
    const fetcher = vi.fn(async () => 'one')
    const { result } = renderHook(() => useRead(fetcher, 'k'))
    expect(result.current.data).toBeNull()
    await waitFor(() => expect(result.current.data).toBe('one'))
    expect(result.current.error).toBe('')
    expect(fetcher).toHaveBeenCalledTimes(1)
  })

  it('reports an error while there is no data', async () => {
    const { result } = renderHook(() => useRead(() => Promise.reject(new Error('boom')), 'k'))
    await waitFor(() => expect(result.current.error).toBe('boom'))
    expect(result.current.data).toBeNull()
  })

  it('keeps the previous data while a refresh is loading', async () => {
    const second = deferred<string>()
    const fetcher = vi.fn().mockResolvedValueOnce('one').mockReturnValueOnce(second.promise)
    const { result } = renderHook(() => useRead(fetcher, 'k'))
    await waitFor(() => expect(result.current.data).toBe('one'))

    act(() => result.current.refresh())
    expect(fetcher).toHaveBeenCalledTimes(2)
    expect(result.current.data).toBe('one')

    await act(async () => second.resolve('two'))
    expect(result.current.data).toBe('two')
  })

  it('keeps the last data when a refresh fails', async () => {
    const fetcher = vi.fn().mockResolvedValueOnce('one').mockRejectedValueOnce(new Error('down'))
    const { result } = renderHook(() => useRead(fetcher, 'k'))
    await waitFor(() => expect(result.current.data).toBe('one'))

    await act(async () => result.current.refresh())
    expect(fetcher).toHaveBeenCalledTimes(2)
    expect(result.current.data).toBe('one')
    expect(result.current.error).toBe('')
  })

  it('starts over when the key changes', async () => {
    const fetcher = vi.fn(async (_s: AbortSignal) => 'x')
    const { result, rerender } = renderHook(({ k }) => useRead(fetcher, k), {
      initialProps: { k: 'a' },
    })
    await waitFor(() => expect(result.current.data).toBe('x'))
    rerender({ k: 'b' })
    expect(result.current.data).toBeNull()
    await waitFor(() => expect(result.current.data).toBe('x'))
    expect(fetcher).toHaveBeenCalledTimes(2)
  })

  it('reads nothing with a null key, and refresh does nothing then', () => {
    const fetcher = vi.fn(async () => 'x')
    const { result } = renderHook(() => useRead(fetcher, null))
    act(() => result.current.refresh())
    expect(fetcher).not.toHaveBeenCalled()
    expect(result.current.data).toBeNull()
  })

  it('aborts the read running at unmount', () => {
    let signal: AbortSignal | undefined
    const { unmount } = renderHook(() =>
      useRead((s) => {
        signal = s
        return new Promise(() => {})
      }, 'k'),
    )
    expect(signal?.aborted).toBe(false)
    unmount()
    expect(signal?.aborted).toBe(true)
  })

  it('aborts the read a refresh supersedes', async () => {
    const signals: AbortSignal[] = []
    const { result } = renderHook(() =>
      useRead((s) => {
        signals.push(s)
        return new Promise(() => {})
      }, 'k'),
    )
    act(() => result.current.refresh())
    expect(signals).toHaveLength(2)
    expect(signals[0].aborted).toBe(true)
    expect(signals[1].aborted).toBe(false)
  })

  it('words an error without a message', () => {
    expect(errorMessage(new Error(''))).toBe('Something went wrong.')
    expect(errorMessage('nope')).toBe('Something went wrong.')
    expect(errorMessage(new Error('x'))).toBe('x')
  })
})
