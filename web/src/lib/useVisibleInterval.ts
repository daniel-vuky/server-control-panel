import { useEffect, useRef } from 'react'

/**
 * useVisibleInterval calls fn every ms while the page is visible: the timer
 * stops when the tab is hidden, and becoming visible again calls fn at once
 * and restarts the period from there. It never calls fn at mount (the page
 * has just loaded its data). fn may be an inline function: the latest one is
 * called, and a new identity does not restart the timer.
 */
export function useVisibleInterval(fn: () => void, ms: number): void {
  const fnRef = useRef(fn)
  fnRef.current = fn

  useEffect(() => {
    let timer: ReturnType<typeof setInterval> | undefined
    const start = () => {
      timer = setInterval(() => fnRef.current(), ms)
    }
    const stop = () => {
      clearInterval(timer)
      timer = undefined
    }
    const onChange = () => {
      if (document.visibilityState !== 'visible') {
        stop()
        return
      }
      if (timer !== undefined) return // it was visible already
      fnRef.current()
      start()
    }
    if (document.visibilityState === 'visible') start()
    document.addEventListener('visibilitychange', onChange)
    return () => {
      document.removeEventListener('visibilitychange', onChange)
      stop()
    }
  }, [ms])
}
