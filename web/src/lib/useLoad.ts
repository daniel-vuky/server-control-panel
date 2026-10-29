import { useCallback, useEffect, useRef, useState } from 'react'

export type Load<T> =
  | { state: 'loading' }
  | { state: 'error'; message: string }
  | { state: 'ok'; data: T }

/**
 * useLoad runs fetcher on mount and whenever retry is called, and aborts the
 * request it supersedes or that is still running at unmount. The fetcher may
 * be an inline function: only the latest one is kept, and a new identity does
 * not refetch.
 */
export function useLoad<T>(fetcher: (signal: AbortSignal) => Promise<T>): {
  load: Load<T>
  retry: () => void
} {
  const [load, setLoad] = useState<Load<T>>({ state: 'loading' })
  const [attempt, setAttempt] = useState(0)
  const fetcherRef = useRef(fetcher)
  fetcherRef.current = fetcher

  // biome-ignore lint/correctness/useExhaustiveDependencies: attempt re-runs the fetch on retry.
  useEffect(() => {
    const ctl = new AbortController()
    setLoad({ state: 'loading' })
    fetcherRef.current(ctl.signal).then(
      (data) => {
        if (!ctl.signal.aborted) setLoad({ state: 'ok', data })
      },
      (err: unknown) => {
        if (ctl.signal.aborted) return
        const message = err instanceof Error && err.message ? err.message : 'Something went wrong.'
        setLoad({ state: 'error', message })
      },
    )
    return () => ctl.abort()
  }, [attempt])

  const retry = useCallback(() => setAttempt((n) => n + 1), [])
  return { load, retry }
}
