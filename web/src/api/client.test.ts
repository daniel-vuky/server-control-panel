import { describe, expect, it, vi } from 'vitest'
import { ApiError, getJSON, postJSON } from './client'

const reply = (status: number, body: string) =>
  vi.fn(async () => new Response(body, { status, headers: { 'Content-Type': 'application/json' } }))

describe('api client', () => {
  it('returns the parsed body on 2xx', async () => {
    vi.stubGlobal('fetch', reply(200, '{"a":1}'))
    await expect(getJSON<{ a: number }>('/api/x')).resolves.toEqual({ a: 1 })
  })

  it('throws ApiError with status and parsed body on 422', async () => {
    const body = { error: 'This folder does not exist.', field: 'path', code: 'not_found' }
    vi.stubGlobal('fetch', reply(422, JSON.stringify(body)))
    const err = await postJSON('/api/scans', { path: '/nope', depth: 1 }).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).status).toBe(422)
    expect((err as ApiError).body).toEqual(body)
    expect((err as ApiError).message).toBe('This folder does not exist.')
  })

  it('keeps a null body when the error body is not JSON', async () => {
    vi.stubGlobal('fetch', reply(502, '<html>bad gateway</html>'))
    const err = await getJSON('/api/x').catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).status).toBe(502)
    expect((err as ApiError).body).toBeNull()
  })

  it('posts the body as JSON', async () => {
    const fetchMock = reply(202, '{"id":7}')
    vi.stubGlobal('fetch', fetchMock)
    await postJSON('/api/scans', { path: '/srv', depth: 2 })
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('/api/scans')
    expect(init.method).toBe('POST')
    expect(init.body).toBe('{"path":"/srv","depth":2}')
    expect(new Headers(init.headers).get('Content-Type')).toBe('application/json')
  })
})
