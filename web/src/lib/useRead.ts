import { useCallback, useEffect, useRef, useState } from 'react'

/** errorMessage is an error's words, or "Something went wrong." when it has none. */
export const errorMessage = (err: unknown) =>
  err instanceof Error && err.message ? err.message : 'Something went wrong.'

/**
 * useRead reads with fetcher at mount and whenever key changes (starting
 * over from nothing), and again on refresh, which keeps what it has until
 * the answer comes: a failed refresh keeps the last data, and the error is
 * reported only while there is no data to show. A null key reads nothing
 * (and refresh does nothing) until it is set. It aborts the read it
 * supersedes and the one running at unmount. The fetcher may be an inline
 * function: the latest one is used, and a new identity does not re-read.
 */
export function useRead<T>(fetcher: (signal: AbortSignal) => Promise<T>, key: string | null) {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState('')
  const fetcherRef = useRef(fetcher)
  fetcherRef.current = fetcher
  const keyRef = useRef(key)
  keyRef.current = key
  const ctl = useRef<AbortController | null>(null)

  const refresh = useCallback(() => {
    if (keyRef.current === null) return
    ctl.current?.abort()
    const c = new AbortController()
    ctl.current = c
    fetcherRef.current(c.signal).then(
      (d) => {
        if (c.signal.aborted) return
        setData(d)
        setError('')
      },
      (err: unknown) => {
        if (!c.signal.aborted) setError(errorMessage(err))
      },
    )
  }, [])

  useEffect(() => {
    ctl.current?.abort()
    setData(null)
    setError('')
    if (key !== null) refresh()
  }, [key, refresh])
  useEffect(() => () => ctl.current?.abort(), [])

  return { data, error: data === null ? error : '', refresh }
}
