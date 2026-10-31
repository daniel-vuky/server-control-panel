import { act } from '@testing-library/react'

/**
 * showPage fires the window's pageshow event, as a browser does on Back: with
 * persisted true when the page comes from the back/forward cache.
 */
export function showPage(persisted: boolean) {
  const e = new Event('pageshow')
  Object.defineProperty(e, 'persisted', { value: persisted })
  act(() => {
    window.dispatchEvent(e)
  })
}
