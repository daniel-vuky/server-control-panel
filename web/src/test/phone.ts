import { vi } from 'vitest'

/**
 * stubPhone makes matchMedia answer for a phone (`max-width: 639px`) or not,
 * since jsdom has none. It returns a setter that flips the answer and tells
 * the listeners, as a window resize would. Undo with vi.unstubAllGlobals().
 */
export function stubPhone(phone: boolean) {
  const listeners = new Set<() => void>()
  let matches = phone
  vi.stubGlobal(
    'matchMedia',
    vi.fn((query: string) => ({
      get matches() {
        return query.includes('max-width: 639px') && matches
      },
      media: query,
      addEventListener: (_: string, fn: () => void) => listeners.add(fn),
      removeEventListener: (_: string, fn: () => void) => listeners.delete(fn),
    })),
  )
  return (next: boolean) => {
    matches = next
    for (const fn of listeners) fn()
  }
}
