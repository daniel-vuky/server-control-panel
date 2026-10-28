import type { MeResponse } from './types.gen'

// fetchMe asks the API who is signed in. It resolves null on any failure
// (network error, non-OK status, a non-JSON body such as an expired-session
// login page, or a missing or non-string email) and never throws.
export async function fetchMe(signal: AbortSignal): Promise<MeResponse | null> {
  try {
    const res = await fetch('/api/me', { signal, headers: { Accept: 'application/json' } })
    if (!res.ok) return null
    const body: unknown = await res.json()
    if (typeof body !== 'object' || body === null) return null
    const email = (body as { email?: unknown }).email
    if (typeof email !== 'string' || email === '') return null
    return { email }
  } catch {
    return null
  }
}
