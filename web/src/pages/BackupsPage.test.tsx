import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { useLocation } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Backup, BackupStep, DriveStatus } from '../api/types.gen'
import { FakeEventSource } from '../test/fakeEventSource'
import { makeBackup, makeProject, pushState } from '../test/projects'
import { renderApp } from '../test/renderApp'
import BackupsPage from './BackupsPage'

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status })

const connected: DriveStatus = {
  state: 'connected',
  email: 'me@example.com',
  folder: { id: 'f', name: 'server-backups' },
  reason: '',
}
const drive = (state: string): DriveStatus => ({ state, email: '', folder: null, reason: '' })

const uploading: BackupStep[] = [
  { step: 'dump', state: 'done', detail: 'pg_dump · 18s' },
  { step: 'compress', state: 'done', detail: 'gzip · 212 → 38 MB' },
  { step: 'upload', state: 'running', detail: '23.6 / 38 MB · 62%' },
  { step: 'prune', state: 'waiting', detail: '' },
]

function Where() {
  const loc = useLocation()
  return <div data-testid="where">{loc.pathname}</div>
}

function open(state: DriveStatus | null, backups: Backup[] = []) {
  renderApp('/backups', {
    routes: [
      { path: '/backups', element: <BackupsPage /> },
      { path: '*', element: <Where /> },
    ],
  })
  if (state !== null) pushState([makeProject()], true, { drive: state, backups })
}

let assign: ReturnType<typeof vi.fn>

beforeEach(() => {
  FakeEventSource.instances = []
  vi.stubGlobal('EventSource', FakeEventSource)
  assign = vi.fn()
  vi.stubGlobal('location', { ...window.location, assign })
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('BackupsPage', () => {
  it('names the Drive folder when connected, with no banner', () => {
    open(connected)
    expect(screen.getByRole('heading', { level: 1, name: 'Backups' })).toBeInTheDocument()
    expect(screen.getByText(/^Stored in/)).toHaveTextContent('Stored in Drive/server-backups')
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('shows the heading before the live state arrives, without banners', () => {
    open(null)
    expect(screen.getByRole('heading', { level: 1, name: 'Backups' })).toBeInTheDocument()
    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.queryByText(/^Stored in/)).toBeNull()
  })

  it('offers Connect Google Drive when not connected, returning here', async () => {
    const stub = vi.fn(async () => json(200, { url: 'https://accounts.google.com/x' }))
    vi.stubGlobal('fetch', stub)
    open(drive('not_connected'))
    const banner = screen.getByRole('alert')
    expect(banner).toHaveTextContent('Google Drive not connected')
    expect(banner).toHaveTextContent(
      'Backups need a destination. Connect once and all projects share the same target folder.',
    )
    expect(screen.queryByText(/^Stored in/)).toBeNull()

    fireEvent.click(within(banner).getByRole('button', { name: 'Connect Google Drive' }))
    await waitFor(() => expect(assign).toHaveBeenCalledWith('https://accounts.google.com/x'))
    const [url, init] = stub.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('/api/drive/connect')
    expect(JSON.parse(String(init.body))).toEqual({ returnTo: '/backups' })
  })

  it('offers Reconnect Google Drive when the token expired', async () => {
    const stub = vi.fn(async () => json(200, { url: 'https://accounts.google.com/y' }))
    vi.stubGlobal('fetch', stub)
    open(drive('expired'))
    const banner = screen.getByRole('alert')
    expect(banner).toHaveTextContent('Google Drive token has expired.')
    expect(banner).toHaveTextContent('Backups will fail until you reconnect.')
    fireEvent.click(within(banner).getByRole('button', { name: 'Reconnect Google Drive' }))
    await waitFor(() => expect(assign).toHaveBeenCalledWith('https://accounts.google.com/y'))
    const [, init] = stub.mock.calls[0] as unknown as [string, RequestInit]
    expect(JSON.parse(String(init.body))).toEqual({ returnTo: '/backups' })
  })

  it('says why the connect failed', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        json(503, { error: 'unreachable', message: 'Google Drive is unreachable.' }),
      ),
    )
    open(drive('not_connected'))
    fireEvent.click(screen.getByRole('button', { name: 'Connect Google Drive' }))
    expect(await screen.findByText('Google Drive is unreachable.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Connect Google Drive' })).toBeEnabled()
    expect(assign).not.toHaveBeenCalled()
  })

  it('says Drive is not set up, without a Connect button', () => {
    open(drive('not_configured'))
    expect(screen.getByText('Google Drive is not set up on this server.')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Connect Google Drive/ })).toBeNull()
  })

  it('shows one card per active backup, running first', () => {
    const waiting = makeBackup({
      id: 8,
      projectId: 2,
      projectName: 'crm',
      database: { ...makeBackup().database, name: 'crm_db' },
      queuePosition: 1,
      createdAt: new Date(2026, 9, 31, 10, 44).toISOString(),
    })
    const running = makeBackup({
      id: 7,
      state: 'running',
      queuePosition: 0,
      steps: uploading,
      startedBy: 'admin@company.vn',
      createdAt: new Date(2026, 9, 31, 10, 42).toISOString(),
    })
    open(connected, [waiting, running])

    const cards = screen.getAllByRole('region', { name: /^(Backing up|Waiting to back up)/ })
    expect(cards).toHaveLength(2)

    expect(cards[0]).toHaveAccessibleName('Backing up shop · shop')
    expect(cards[0]).toHaveTextContent('Triggered by admin@company.vn · 10:42')
    const steps = within(cards[0]).getAllByRole('listitem')
    expect(steps.map((s) => s.getAttribute('data-state'))).toEqual([
      'done',
      'done',
      'running',
      'waiting',
    ])
    expect(within(cards[0]).getByRole('link', { name: 'shop' })).toHaveAttribute(
      'href',
      '/projects/1/database',
    )

    expect(cards[1]).toHaveAccessibleName('Waiting to back up crm · crm_db')
    expect(cards[1]).toHaveTextContent('Waiting · 1 before it')
    expect(cards[1]).toHaveTextContent('Triggered by dev@localhost · 10:44')
    expect(within(cards[1]).getByRole('link', { name: 'crm' })).toHaveAttribute(
      'href',
      '/projects/2/database',
    )

    expect(screen.queryByText('No backup is running.')).toBeNull()
  })

  it('says when nothing is running', () => {
    open(connected)
    expect(screen.getByText('No backup is running.')).toBeInTheDocument()
    expect(
      screen.getByText('The list of all backups arrives with the next backup flow.'),
    ).toBeInTheDocument()
  })
})
