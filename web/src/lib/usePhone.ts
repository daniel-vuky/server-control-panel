import { useSyncExternalStore } from 'react'

/** PHONE is the phone layout's breakpoint: below Tailwind's `sm` (640 px). */
const PHONE = '(max-width: 639px)'

function subscribe(onChange: () => void): () => void {
  if (typeof window.matchMedia !== 'function') return () => {}
  const query = window.matchMedia(PHONE)
  query.addEventListener('change', onChange)
  return () => query.removeEventListener('change', onChange)
}

const snapshot = () => typeof window.matchMedia === 'function' && window.matchMedia(PHONE).matches

/**
 * usePhone says whether the window is a phone (below 640 px). It is for what
 * CSS alone cannot do, such as swapping the table view for cards; layout
 * itself uses `max-sm:` classes. Without matchMedia (jsdom) it reads wide.
 */
export function usePhone(): boolean {
  return useSyncExternalStore(subscribe, snapshot, () => false)
}
