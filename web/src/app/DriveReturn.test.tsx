import { screen, waitFor } from '@testing-library/react'
import { useLocation } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { DriveStatus } from '../api/types.gen'
import { FakeEventSource } from '../test/fakeEventSource'
import { pushState } from '../test/projects'
import { renderApp } from '../test/renderApp'
import DriveReturn from './DriveReturn'

const connected: DriveStatus = {
  state: 'connected',
  email: 'ops.backup@gmail.com',
  folder: { id: 'f1', name: 'server-backups' },
  reason: '',
}
const notConnected: DriveStatus = { state: 'not_connected', email: '', folder: null, reason: '' }

function Where() {
  const loc = useLocation()
  return <div data-testid="where">{`${loc.pathname}${loc.search}`}</div>
}

function renderAt(path: string, drive: DriveStatus) {
  renderApp(path, {
    routes: [
      {
        path: '*',
        element: (
          <>
            <DriveReturn />
            <Where />
          </>
        ),
      },
    ],
  })
  pushState([], true, { drive })
}

beforeEach(() => {
  FakeEventSource.instances = []
  vi.stubGlobal('EventSource', FakeEventSource)
})
afterEach(() => vi.unstubAllGlobals())

describe('coming back from Google', () => {
  it('?drive=connected toasts the account and clears the query', async () => {
    renderAt('/settings?drive=connected', connected)
    expect(await screen.findByTestId('toast-success')).toHaveTextContent(
      'Connected Google Drive as ops.backup@gmail.com',
    )
    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent(/^\/settings$/))
  })

  it('?drive=declined toasts that it was not connected', async () => {
    renderAt('/settings?drive=declined', notConnected)
    expect(await screen.findByRole('alert')).toHaveTextContent('Google Drive was not connected.')
    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent(/^\/settings$/))
  })

  it('?drive=error shows the code words and the code, and keeps other parameters', async () => {
    renderAt('/settings?drive=error&code=unreachable&x=1', notConnected)
    const toast = await screen.findByRole('alert')
    expect(toast).toHaveTextContent('Google Drive was not connected.')
    expect(toast).toHaveTextContent('Google Drive is unreachable.')
    expect(toast).toHaveTextContent('unreachable')
    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent(/^\/settings\?x=1$/))
  })

  it('works on a project page, keeping its own query', async () => {
    renderAt('/projects/3/database?backup=postgresql:db:shop&drive=connected', connected)
    expect(await screen.findByTestId('toast-success')).toHaveTextContent(
      'Connected Google Drive as ops.backup@gmail.com',
    )
    await waitFor(() =>
      expect(screen.getByTestId('where')).toHaveTextContent(
        '/projects/3/database?backup=postgresql%3Adb%3Ashop',
      ),
    )
    expect(screen.getAllByTestId('toast-success')).toHaveLength(1)
  })
})
