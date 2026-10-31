import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { useLocation } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Backup, DriveStatus, Project } from '../api/types.gen'
import { FakeEventSource } from '../test/fakeEventSource'
import { showPage } from '../test/pageshow'
import { makeBackup, makeDatabase, makeProject, pushState } from '../test/projects'
import { renderApp } from '../test/renderApp'
import { useBackup } from './useBackup'

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status })

const allowed = { allowed: true, reason: '', service: '' }
const shopDb = makeDatabase({ name: 'shop', backup: allowed })
const SHOP_KEY = 'postgresql:db:shop'

const connected: DriveStatus = {
  state: 'connected',
  email: 'me@example.com',
  folder: { id: 'f', name: 'server-backups' },
  reason: '',
}

type Route = (url: string, init?: RequestInit) => Promise<Response> | Response | undefined

/** serve answers fetch with the first route that answers; the rest is a 404. */
function serve(...routes: Route[]) {
  const stub = vi.fn(async (url: string, init?: RequestInit) => {
    for (const r of routes) {
      const res = await r(url, init)
      if (res) return res
    }
    return json(404, { error: 'not found' })
  })
  vi.stubGlobal('fetch', stub)
  return stub
}

const post =
  (url: string, answer: () => Promise<Response> | Response): Route =>
  (u, init) =>
    u === url && init?.method === 'POST' ? answer() : undefined
const get =
  (url: string, answer: () => Response): Route =>
  (u, init) =>
    u === url && init?.method !== 'POST' ? answer() : undefined

const calls = (stub: ReturnType<typeof vi.fn>, url: string, method?: string) =>
  stub.mock.calls.filter(
    (c) => c[0] === url && (method === undefined || (c[1] as RequestInit)?.method === method),
  )

function Harness({ project }: { project: Project }) {
  const b = useBackup()
  return (
    <>
      <button type="button" onClick={() => b.start(project)}>
        Go
      </button>
      <button type="button" onClick={() => b.open(7)}>
        Open 7
      </button>
      <span data-testid="pending">{b.pendingKey ?? ''}</span>
      <span data-testid="refusals">{b.refusals}</span>
      {b.dialogs}
    </>
  )
}

function Where() {
  const loc = useLocation()
  return <div data-testid="where">{`${loc.pathname}${loc.search}`}</div>
}

function setup(project: Project, drive: DriveStatus = connected, backups: Backup[] = []) {
  const r = renderApp('/', {
    routes: [
      { path: '/', element: <Harness project={project} /> },
      { path: '*', element: <Where /> },
    ],
  })
  pushState([project], true, { drive, backups })
  return r
}

const go = () => fireEvent.click(screen.getByRole('button', { name: 'Go' }))

let assign: ReturnType<typeof vi.fn>

beforeEach(() => {
  FakeEventSource.instances = []
  vi.stubGlobal('EventSource', FakeEventSource)
  assign = vi.fn()
  vi.stubGlobal('location', { ...window.location, assign })
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('useBackup', () => {
  it('starts the one database at once and opens its progress', async () => {
    const stub = serve(
      post('/api/projects/1/backups', () => json(202, { id: 7 })),
      get('/api/backups/7', () => json(200, makeBackup({ queuePosition: 2 }))),
    )
    setup(makeProject({ databases: [shopDb] }))
    go()
    const dialog = await screen.findByRole('dialog', { name: 'Backup shop · shop' })
    const [, init] = calls(stub, '/api/projects/1/backups', 'POST')[0]
    expect(JSON.parse(String((init as RequestInit).body))).toEqual({ database: SHOP_KEY })
    expect((init as RequestInit).headers).toMatchObject({ 'Content-Type': 'application/json' })
    expect(await within(dialog).findByText('Waiting · 2 before it')).toBeInTheDocument()
  })

  it('opens the chooser for several databases, with disabled rows and their reasons', async () => {
    const stub = serve(
      post('/api/projects/1/backups', () => json(202, { id: 7 })),
      get('/api/backups/7', () => json(200, makeBackup())),
    )
    const project = makeProject({
      databases: [
        makeDatabase({
          type: 'mysql',
          name: 'crm_db',
          location: 'mysql',
          backup: { allowed: false, reason: 'db_not_running', service: 'mysql' },
        }),
        makeDatabase({ name: 'shop', backup: allowed }),
        makeDatabase({
          type: 'sqlite',
          name: 'cache',
          locationKind: 'file',
          location: './cache.db',
          backup: { allowed: false, reason: 'not_supported', service: '' },
        }),
      ],
    })
    setup(project)
    go()
    const dialog = screen.getByRole('dialog', { name: 'Backup shop' })
    expect(dialog).toHaveTextContent('This project has 3 databases. Choose the one to back up.')
    const crm = within(dialog).getByRole('radio', { name: 'crm_db' })
    expect(crm).toBeDisabled()
    expect(within(dialog).getByText('Start mysql first')).toBeInTheDocument()
    expect(within(dialog).getByText('SQLite backups are not supported yet')).toBeInTheDocument()
    expect(within(dialog).getByText('MySQL · container mysql')).toBeInTheDocument()
    // The first database that can be backed up is chosen.
    expect(within(dialog).getByRole('radio', { name: 'shop' })).toBeChecked()
    expect(calls(stub, '/api/projects/1/backups')).toHaveLength(0)

    fireEvent.click(within(dialog).getByRole('button', { name: 'Back up now' }))
    await screen.findByRole('dialog', { name: 'Backup shop · shop' })
    const [, init] = calls(stub, '/api/projects/1/backups', 'POST')[0]
    expect(JSON.parse(String((init as RequestInit).body))).toEqual({ database: SHOP_KEY })
    expect(screen.queryByRole('dialog', { name: 'Backup shop' })).toBeNull()
  })

  it('cancels the chooser without starting anything', () => {
    const stub = serve()
    setup(
      makeProject({
        databases: [
          makeDatabase({ name: 'a', backup: allowed }),
          makeDatabase({ name: 'b', backup: allowed }),
        ],
      }),
    )
    go()
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(stub).not.toHaveBeenCalledWith('/api/projects/1/backups', expect.anything())
  })

  it.each([
    ['not_connected', 'drive_not_connected'],
    ['expired', 'drive_expired'],
  ])('asks to connect Drive (%s) and comes back to the database tab', async (state, reason) => {
    const stub = serve(
      post('/api/drive/connect', () => json(200, { url: 'https://accounts.google.com/x' })),
    )
    setup(
      makeProject({
        databases: [
          makeDatabase({ name: 'shop', backup: { allowed: false, reason, service: '' } }),
        ],
      }),
      {
        ...connected,
        state,
      },
    )
    go()
    const dialog = screen.getByRole('dialog', { name: 'Connect Google Drive first' })
    expect(dialog).toHaveTextContent(
      'Backups need a storage location. After connecting, you will return here and continue backing up shop.',
    )
    fireEvent.click(within(dialog).getByRole('button', { name: 'Connect Google Drive' }))
    await waitFor(() => expect(assign).toHaveBeenCalledWith('https://accounts.google.com/x'))
    const [, init] = calls(stub, '/api/drive/connect', 'POST')[0]
    expect(JSON.parse(String((init as RequestInit).body))).toEqual({
      returnTo: `/projects/1/database?backup=${encodeURIComponent(SHOP_KEY)}`,
    })
    expect(calls(stub, '/api/projects/1/backups')).toHaveLength(0)
    // Back from the consent page restores this page from the cache: Connect works again.
    expect(within(dialog).getByRole('button', { name: 'Connect Google Drive' })).toBeDisabled()
    showPage(true)
    expect(within(dialog).getByRole('button', { name: 'Connect Google Drive' })).toBeEnabled()
  })

  it('closes the Drive dialog with Later', () => {
    serve()
    setup(
      makeProject({
        databases: [
          makeDatabase({
            name: 'shop',
            backup: { allowed: false, reason: 'drive_not_connected', service: '' },
          }),
        ],
      }),
      { ...connected, state: 'not_connected', folder: null },
    )
    go()
    fireEvent.click(screen.getByRole('button', { name: 'Later' }))
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('opens the Drive dialog from the chooser for a Drive reason', () => {
    serve()
    const drive = { allowed: false, reason: 'drive_expired', service: '' }
    setup(
      makeProject({
        databases: [
          makeDatabase({ name: 'a', backup: drive }),
          makeDatabase({ name: 'b', backup: drive }),
        ],
      }),
      { ...connected, state: 'expired' },
    )
    go()
    const chooser = screen.getByRole('dialog', { name: 'Backup shop' })
    fireEvent.click(within(chooser).getByRole('radio', { name: 'b' }))
    fireEvent.click(within(chooser).getByRole('button', { name: 'Back up now' }))
    expect(screen.getByRole('dialog', { name: 'Connect Google Drive first' })).toHaveTextContent(
      'continue backing up b.',
    )
  })

  it('shows a pending state while the POST runs and ignores a second start', async () => {
    let answer: (r: Response) => void = () => {}
    const stub = serve(
      post('/api/projects/1/backups', () => new Promise<Response>((res) => (answer = res))),
      get('/api/backups/7', () => json(200, makeBackup())),
    )
    setup(makeProject({ databases: [shopDb] }))
    go()
    expect(screen.getByTestId('pending')).toHaveTextContent(SHOP_KEY)
    go()
    expect(calls(stub, '/api/projects/1/backups', 'POST')).toHaveLength(1)
    await act(async () => answer(json(202, { id: 7 })))
    expect(screen.getByTestId('pending')).toHaveTextContent('')
    expect(await screen.findByRole('dialog', { name: 'Backup shop · shop' })).toBeInTheDocument()
  })

  it('opens the running backup on already_running, with a toast', async () => {
    serve(
      post('/api/projects/1/backups', () =>
        json(409, {
          error: 'already_running',
          message: 'A backup of shop is already running.',
          id: 7,
        }),
      ),
    )
    setup(makeProject({ databases: [shopDb] }), connected, [
      makeBackup({ state: 'running', queuePosition: 0 }),
    ])
    go()
    expect(await screen.findByTestId('toast-success')).toHaveTextContent(
      'A backup of shop is already running.',
    )
    const dialog = screen.getByRole('dialog', { name: 'Backup shop · shop' })
    expect(within(dialog).getByText('Running')).toBeInTheDocument()
  })

  it('words disk_short with the sizes and links to the disk usage', async () => {
    serve(
      post('/api/projects/1/backups', () =>
        json(409, {
          error: 'disk_short',
          message: 'server words',
          needBytes: 2.1 * 1024 ** 3,
          freeBytes: 1.4 * 1024 ** 3,
        }),
      ),
    )
    setup(makeProject({ databases: [shopDb] }))
    go()
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Not enough space to dump')
    expect(alert).toHaveTextContent(
      'Needs about 2.1 GB free, the server has 1.4 GB left. Free up some disk space and try again.',
    )
    const link = within(alert).getByRole('link', { name: 'View disk usage by app' })
    expect(link).toHaveAttribute('href', '/server')
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('treats omitted disk_short sizes as zero', async () => {
    serve(post('/api/projects/1/backups', () => json(409, { error: 'disk_short', message: 'x' })))
    setup(makeProject({ databases: [shopDb] }))
    go()
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Needs about 0 B free, the server has 0 B left.',
    )
  })

  it('opens the Drive dialog when the server says Drive is gone meanwhile', async () => {
    serve(
      post('/api/projects/1/backups', () =>
        json(409, { error: 'drive_expired', message: 'The Google Drive token has expired.' }),
      ),
    )
    setup(makeProject({ databases: [shopDb] }))
    go()
    expect(
      await screen.findByRole('dialog', { name: 'Connect Google Drive first' }),
    ).toBeInTheDocument()
  })

  it.each([
    ['disk_short', 1],
    ['db_not_running', 1],
    ['docker_down', 1],
    ['no_db_name', 1],
    ['not_supported', 1],
    ['drive_not_connected', 1],
    ['drive_expired', 1],
    ['folder_missing', 1],
    ['already_running', 0],
    ['not_found', 0],
    ['not_retryable', 0],
  ])('counts a refusal %s that wrote a history row: %i', async (code, want) => {
    const stub = serve(
      post('/api/projects/1/backups', () => json(409, { error: code, message: 'words', id: 9 })),
      get('/api/backups/9', () => json(200, makeBackup({ id: 9 }))),
    )
    setup(makeProject({ databases: [shopDb] }))
    expect(screen.getByTestId('refusals')).toHaveTextContent('0')
    go()
    await waitFor(() => expect(calls(stub, '/api/projects/1/backups', 'POST')).toHaveLength(1))
    // Let the answer settle.
    await act(async () => {
      await new Promise((r) => setTimeout(r, 0))
    })
    expect(screen.getByTestId('refusals')).toHaveTextContent(String(want))
  })

  it.each([
    [409, { error: 'db_not_running', message: 'Start db first', service: 'db' }, 'Start db first'],
    [
      422,
      { error: 'This database is no longer detected.', field: 'database', code: 'unknown' },
      'This database is no longer detected.',
    ],
    [404, { error: 'This project is not tracked.' }, 'This project is not tracked.'],
  ])('shows any other refusal (%s) as a red toast with its words', async (status, body, words) => {
    serve(post('/api/projects/1/backups', () => json(status, body)))
    setup(makeProject({ databases: [shopDb] }))
    go()
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent("Couldn't back up shop")
    expect(alert).toHaveTextContent(words)
    expect(screen.getByTestId('pending')).toHaveTextContent('')
  })
})
