import { describe, expect, it, vi } from 'vitest'
import { listProjects } from './projects'
import { listWorkspaces, removeWorkspace } from './workspaces'

describe('list helpers', () => {
  it('listWorkspaces reads /api/workspaces', async () => {
    const fetchMock = vi.fn(async () => new Response('[]', { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(listWorkspaces()).resolves.toEqual([])
    expect((fetchMock.mock.calls[0] as unknown[])[0]).toBe('/api/workspaces')
  })

  it('listProjects reads /api/projects', async () => {
    const fetchMock = vi.fn(async () => new Response('[]', { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(listProjects()).resolves.toEqual([])
    expect((fetchMock.mock.calls[0] as unknown[])[0]).toBe('/api/projects')
  })

  it('removeWorkspace posts JSON to /api/workspaces/{id}/remove', async () => {
    const fetchMock = vi.fn(async () => new Response('{"removed":2}', { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)
    await expect(removeWorkspace(3)).resolves.toEqual({ removed: 2 })
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('/api/workspaces/3/remove')
    expect(init.method).toBe('POST')
    expect(init.headers).toMatchObject({ 'Content-Type': 'application/json' })
  })
})
