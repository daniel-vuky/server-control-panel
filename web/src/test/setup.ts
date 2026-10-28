import '@testing-library/jest-dom/vitest'
import { afterEach, beforeEach, vi } from 'vitest'

beforeEach(() => {
  vi.stubGlobal(
    'fetch',
    vi.fn(async () => new Response('{"error":"unauthorized"}', { status: 401 })),
  )
})

afterEach(() => {
  vi.unstubAllGlobals()
})
