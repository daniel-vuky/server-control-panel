import { describe, expect, it, vi } from 'vitest'
import { listProjects } from './projects'
import { listWorkspaces } from './workspaces'

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
})
