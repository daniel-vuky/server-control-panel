import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { DriveStatus } from '../api/types.gen'
import { FakeEventSource } from '../test/fakeEventSource'
import { showPage } from '../test/pageshow'
import { renderApp } from '../test/renderApp'

const json = (code: number, body: unknown) => new Response(JSON.stringify(body), { status: code })
const drive = (state: string): DriveStatus => ({
  state,
  email: state === 'expired' ? 'ops@example.com' : '',
  folder: null,
  reason: '',
})

const EXPIRED = 'Google Drive token has expired.'

const banner = () => {
  const el = screen.getByText(EXPIRED).closest('[role="alert"]')
  if (!(el instanceof HTMLElement)) throw new Error('banner is not an alert')
  return el
}

/** pushDrive sends a live state with this Drive status to the app's one stream. */
function pushDrive(d: DriveStatus) {
  act(() => {
    const es = FakeEventSource.latest('/api/events')
    if (!es) throw new Error('no /api/events source')
    es.emit('state', { dockerUp: true, projects: [], backups: [], batch: null, drive: d })
  })
}

let assign: ReturnType<typeof vi.fn>

beforeEach(() => {
  FakeEventSource.instances = []
  vi.stubGlobal('EventSource', FakeEventSource)
  assign = vi.fn()
  vi.stubGlobal('location', { ...window.location, assign })
})
afterEach(() => vi.unstubAllGlobals())

describe('DriveExpiredBanner', () => {
  it.each(['/settings', '/backups', '/projects/7/logs'])(
    'shows on %s while the token is expired, and hides when it is not',
    (path) => {
      renderApp(path)
      pushDrive(drive('connected'))
      expect(screen.queryByText(EXPIRED)).toBeNull()

      pushDrive(drive('expired'))
      expect(banner()).toHaveTextContent(
        'Google Drive token has expired. Scheduled backups will fail until you reconnect.',
      )
      expect(within(banner()).getByRole('button', { name: 'Reconnect Google Drive' })).toBeEnabled()

      pushDrive(drive('connected'))
      expect(screen.queryByText(EXPIRED)).toBeNull()
    },
  )

  it('is hidden before the first state and for not connected or not set up', () => {
    renderApp('/settings')
    expect(screen.queryByText(EXPIRED)).toBeNull()
    pushDrive(drive('not_connected'))
    expect(screen.queryByText(EXPIRED)).toBeNull()
    pushDrive(drive('not_configured'))
    expect(screen.queryByText(EXPIRED)).toBeNull()
  })

  it('Reconnect starts the connect returning to the current path', async () => {
    const stub = vi.fn(async (url: string) =>
      url === '/api/drive/connect'
        ? json(200, { url: 'https://accounts.google.com/z' })
        : json(401, { error: 'unauthorized' }),
    )
    vi.stubGlobal('fetch', stub)
    renderApp('/projects/7/logs?service=web')
    pushDrive(drive('expired'))
    const button = within(banner()).getByRole('button', { name: 'Reconnect Google Drive' })
    fireEvent.click(button)
    await waitFor(() => expect(assign).toHaveBeenCalledWith('https://accounts.google.com/z'))
    const call = stub.mock.calls.find((c) => c[0] === '/api/drive/connect') as unknown as [
      string,
      RequestInit,
    ]
    expect(call[1].method).toBe('POST')
    expect(JSON.parse(String(call[1].body))).toEqual({ returnTo: '/projects/7/logs?service=web' })
    // Back from the consent page restores the page from the cache: the button works again.
    expect(button).toBeDisabled()
    showPage(true)
    expect(within(banner()).getByRole('button', { name: 'Reconnect Google Drive' })).toBeEnabled()
  })

  it('says why the connect failed', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url: string) =>
        url === '/api/drive/connect'
          ? json(502, { error: 'unreachable', message: 'Google Drive is unreachable.' })
          : json(401, { error: 'unauthorized' }),
      ),
    )
    renderApp('/settings')
    pushDrive(drive('expired'))
    fireEvent.click(within(banner()).getByRole('button', { name: 'Reconnect Google Drive' }))
    expect(await within(banner()).findByText('Google Drive is unreachable.')).toBeInTheDocument()
    expect(within(banner()).getByRole('button', { name: 'Reconnect Google Drive' })).toBeEnabled()
    expect(assign).not.toHaveBeenCalled()
  })
})
