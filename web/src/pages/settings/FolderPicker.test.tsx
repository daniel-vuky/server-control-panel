import { fireEvent, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { DriveStatus } from '../../api/types.gen'
import { renderApp } from '../../test/renderApp'
import FolderPicker from './FolderPicker'

const folders = [
  { id: 'f1', name: 'server-backups' },
  { id: 'f2', name: 'archive' },
]
const drive: DriveStatus = {
  state: 'connected',
  email: 'ops.backup@gmail.com',
  folder: folders[0],
  reason: '',
}

afterEach(() => vi.unstubAllGlobals())

describe('FolderPicker', () => {
  it('without onChange, choosing a folder still posts it and does not throw', async () => {
    const stub = vi.fn(async (url: string, init?: RequestInit) => {
      if (url === '/api/drive/folders') return new Response(JSON.stringify(folders))
      if (url === '/api/drive/folder' && init?.method === 'POST')
        return new Response(JSON.stringify({ ...drive, folder: folders[1] }))
      return new Response('{}', { status: 404 })
    })
    vi.stubGlobal('fetch', stub)
    renderApp('/x', { routes: [{ path: '/x', element: <FolderPicker drive={drive} /> }] })
    await screen.findByRole('option', { name: 'My Drive / archive' })
    fireEvent.change(screen.getByLabelText('Destination folder'), { target: { value: 'f2' } })
    await waitFor(() =>
      expect(
        stub.mock.calls
          .filter((c) => c[0] === '/api/drive/folder')
          .map((c) => (c[1] as RequestInit).body),
      ).toEqual([JSON.stringify({ id: 'f2' })]),
    )
    expect(screen.queryByRole('alert')).toBeNull()
  })
})
