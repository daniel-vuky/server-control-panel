import { useCallback, useEffect, useState } from 'react'

export type Load<T> =
  | { state: 'loading' }
  | { state: 'error'; message: string }
  | { state: 'ok'; data: T }

/**
 * useLoad runs fetcher on mount and whenever retry is called, and aborts the
 * request it supersedes or that is still running at unmount.
 */
export function useLoad<T>(fetcher: (signal: AbortSignal) => Promise<T>): {
  load: Load<T>
  retry: () => void
} {
  const [load, setLoad] = useState<Load<T>>({ state: 'loading' })
  const [attempt, setAttempt] = useState(0)

  // biome-ignore lint/correctness/useExhaustiveDependencies: attempt re-runs the fetch on retry.
  useEffect(() => {
    const ctl = new AbortController()
    setLoad({ state: 'loading' })
    fetcher(ctl.signal).then(
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
  }, [fetcher, attempt])

  const retry = useCallback(() => setAttempt((n) => n + 1), [])
  return { load, retry }
}
