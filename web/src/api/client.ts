/** ApiError is thrown for a non-2xx answer. Body is the parsed JSON body, or null. */
export class ApiError extends Error {
  status: number
  body: unknown

  constructor(status: number, body: unknown, message?: string) {
    super(message ?? errorMessage(status, body))
    this.name = 'ApiError'
    this.status = status
    this.body = body
  }
}

function errorMessage(status: number, body: unknown): string {
  if (typeof body === 'object' && body !== null) {
    const msg = (body as { error?: unknown }).error
    if (typeof msg === 'string' && msg !== '') return msg
  }
  return `Request failed (${status})`
}

async function parseBody(res: Response): Promise<unknown> {
  const text = await res.text()
  if (text === '') return null
  try {
    return JSON.parse(text)
  } catch {
    // A 2xx that is not JSON is not an API answer: an expired Cloudflare
    // Access session returns an HTML login page this way.
    if (res.ok) throw new ApiError(res.status, null, 'Unexpected response from the server.')
    return null
  }
}

async function request<T>(url: string, init: RequestInit): Promise<T> {
  const res = await fetch(url, init)
  const body = await parseBody(res)
  if (!res.ok) throw new ApiError(res.status, body)
  return body as T
}

export function getJSON<T>(url: string, signal?: AbortSignal): Promise<T> {
  return request<T>(url, { signal, headers: { Accept: 'application/json' } })
}

export function postJSON<T>(url: string, body: unknown, signal?: AbortSignal): Promise<T> {
  return request<T>(url, {
    method: 'POST',
    signal,
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}
