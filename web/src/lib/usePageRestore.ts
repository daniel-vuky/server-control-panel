import { useEffect, useRef } from 'react'

/**
 * usePageRestore calls fn when the browser shows this page again from its
 * back/forward cache (pageshow with persisted), as on Back from Google's
 * consent page. The page then keeps its old state, so a Connect button left
 * busy before the redirect uses it to become usable again.
 */
export function usePageRestore(fn: () => void) {
  const latest = useRef(fn)
  latest.current = fn
  useEffect(() => {
    const onShow = (e: PageTransitionEvent) => {
      if (e.persisted) latest.current()
    }
    window.addEventListener('pageshow', onShow)
    return () => window.removeEventListener('pageshow', onShow)
  }, [])
}
