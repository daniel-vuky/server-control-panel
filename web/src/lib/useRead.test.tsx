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

  it('reports a failed refresh as staleError while it keeps the data, and clears it on success', async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce('one')
      .mockRejectedValueOnce(new Error('down'))
      .mockResolvedValueOnce('two')
    const { result } = renderHook(() => useRead(fetcher, 'k'))
    await waitFor(() => expect(result.current.data).toBe('one'))
    expect(result.current.staleError).toBe('')

    await act(async () => result.current.refresh())
    expect(result.current.data).toBe('one')
    expect(result.current.staleError).toBe('down')
    expect(result.current.error).toBe('')

    await act(async () => result.current.refresh())
    expect(result.current.data).toBe('two')
    expect(result.current.staleError).toBe('')
  })

  it('has no staleError while there is no data', async () => {
    const { result } = renderHook(() => useRead(() => Promise.reject(new Error('boom')), 'k'))
    await waitFor(() => expect(result.current.error).toBe('boom'))
    expect(result.current.staleError).toBe('')
  })

  it('drops the staleError when the key changes', async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce('one')
      .mockRejectedValueOnce(new Error('down'))
      .mockReturnValueOnce(new Promise(() => {}))
    const { result, rerender } = renderHook(({ k }) => useRead(fetcher, k), {
      initialProps: { k: 'a' },
    })
    await waitFor(() => expect(result.current.data).toBe('one'))
    await act(async () => result.current.refresh())
    expect(result.current.staleError).toBe('down')
    rerender({ k: 'b' })
    expect(result.current.staleError).toBe('')
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

  it('is pending from a read until its answer, success or failure', async () => {
    const first = deferred<string>()
    const second = deferred<string>()
    const fetcher = vi.fn().mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)
    const { result } = renderHook(() => useRead(fetcher, 'k'))
    expect(result.current.pending).toBe(true)
    await act(async () => first.reject(new Error('down')))
    expect(result.current.pending).toBe(false)
    expect(result.current.error).toBe('down')

    act(() => result.current.refresh())
    expect(result.current.pending).toBe(true)
    expect(result.current.error).toBe('down')
    await act(async () => second.resolve('one'))
    expect(result.current.pending).toBe(false)
    expect(result.current.data).toBe('one')
  })

  it('is not pending with a null key', () => {
    const { result } = renderHook(() =>
      useRead(
        vi.fn(async () => 'x'),
        null,
      ),
    )
    expect(result.current.pending).toBe(false)
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
