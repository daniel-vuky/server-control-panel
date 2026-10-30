import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { useLocation } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { DriveStatus } from '../../api/types.gen'
import { FakeEventSource } from '../../test/fakeEventSource'
import { pushState } from '../../test/projects'
import { renderApp } from '../../test/renderApp'
import DriveCard from './DriveCard'

const status = (over: Partial<DriveStatus> = {}): DriveStatus => ({
  state: 'connected',
  email: 'ops.backup@gmail.com',
  folder: { id: 'f1', name: 'server-backups' },
  reason: '',
  ...over,
})

const json = (code: number, body: unknown) => new Response(JSON.stringify(body), { status: code })

type Handler = (init?: RequestInit) => Response

/** route stubs fetch by "METHOD /path"; anything else answers 404. */
function route(table: Record<string, Handler>) {
  const stub = vi.fn(async (url: string, init?: RequestInit) => {
    const key = `${init?.method ?? 'GET'} ${url}`
    const h = table[key]
    return h ? h(init) : json(404, { error: `unrouted ${key}` })
  })
  vi.stubGlobal('fetch', stub)
  return stub
}

const posts = (stub: ReturnType<typeof vi.fn>, url: string) =>
  stub.mock.calls
    .filter((c) => c[0] === url && (c[1] as RequestInit | undefined)?.method === 'POST')
    .map((c) => JSON.parse(String((c[1] as RequestInit).body)))

function Where() {
  const loc = useLocation()
  return <div data-testid="where">{`${loc.pathname}${loc.search}`}</div>
}

const folders = [
  { id: 'f1', name: 'server-backups' },
  { id: 'f2', name: 'archive' },
]

function renderCard(drive: DriveStatus, path = '/settings') {
  const r = renderApp(path, {
    routes: [
      {
        path: '/settings',
        element: (
          <>
            <DriveCard />
            <Where />
          </>
        ),
      },
    ],
  })
  pushState([], true, { drive })
  return r
}

let assign: ReturnType<typeof vi.fn>

beforeEach(() => {
  FakeEventSource.instances = []
  vi.stubGlobal('EventSource', FakeEventSource)
  assign = vi.fn()
  vi.stubGlobal('location', { ...window.location, assign })
})
afterEach(() => vi.unstubAllGlobals())

describe('Google Drive card', () => {
  it('not_configured says so, links the guide and has no buttons', () => {
    route({})
    renderCard(
      status({
        state: 'not_configured',
        email: '',
        folder: null,
        reason: 'GOOGLE_CLIENT_ID is not set',
      }),
    )
    expect(screen.getByText('Google Drive is not set up on this server.')).toBeInTheDocument()
    expect(screen.getByText('GOOGLE_CLIENT_ID is not set')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /set up Google Drive/ })).toHaveAttribute(
      'href',
      'https://github.com/daniel-vuky/server-control-panel/blob/main/docs/google-drive.md',
    )
    expect(screen.queryAllByRole('button')).toHaveLength(0)
  })

  it('not_connected offers Connect, which posts the return path and navigates to Google', async () => {
    const stub = route({
      'POST /api/drive/connect': () =>
        json(200, { url: 'https://accounts.google.com/o/oauth2/auth?x=1' }),
    })
    renderCard(status({ state: 'not_connected', email: '', folder: null }))
    expect(
      screen.getByText(
        'Access is limited to files created by the app. Tokens are encrypted at rest.',
      ),
    ).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Connect Google Drive' }))
    await waitFor(() =>
      expect(assign).toHaveBeenCalledWith('https://accounts.google.com/o/oauth2/auth?x=1'),
    )
    expect(posts(stub, '/api/drive/connect')).toEqual([{ returnTo: '/settings' }])
  })

  it('a failed Connect shows the words and the code', async () => {
    route({
      'POST /api/drive/connect': () =>
        json(409, {
          error: 'not_configured',
          message: 'Google Drive is not set up on this server.',
        }),
    })
    renderCard(status({ state: 'not_connected', email: '', folder: null }))
    fireEvent.click(screen.getByRole('button', { name: 'Connect Google Drive' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Google Drive is not set up on this server.')
    expect(alert).toHaveTextContent('not_configured')
    expect(assign).not.toHaveBeenCalled()
  })

  it('expired shows the red line and the Testing note, and Reconnect connects', async () => {
    const stub = route({
      'POST /api/drive/connect': () => json(200, { url: 'https://accounts.google.com/x' }),
    })
    renderCard(status({ state: 'expired' }))
    expect(
      screen.getByText(
        'The Google Drive token has expired. Backups will fail until you reconnect.',
      ),
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        'If the Google Cloud app is still in Testing, publish it: tokens issued in Testing expire after 7 days.',
      ),
    ).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Reconnect Google Drive' }))
    await waitFor(() => expect(assign).toHaveBeenCalledWith('https://accounts.google.com/x'))
    expect(posts(stub, '/api/drive/connect')).toEqual([{ returnTo: '/settings' }])
  })

  it('connected shows the account and the folder, and selecting another folder posts its id', async () => {
    const stub = route({
      'GET /api/drive/folders': () => json(200, folders),
      'POST /api/drive/folder': () => json(200, status({ folder: folders[1] })),
    })
    renderCard(status())
    expect(screen.getByText('ops.backup@gmail.com')).toBeInTheDocument()
    const select = screen.getByLabelText('Destination folder')
    await screen.findByRole('option', { name: 'My Drive / archive' })
    expect(select).toHaveValue('f1')
    fireEvent.change(select, { target: { value: 'f2' } })
    await waitFor(() => expect(select).toHaveValue('f2'))
    expect(posts(stub, '/api/drive/folder')).toEqual([{ id: 'f2' }])
  })

  it('creates a folder by name; a 422 shows its message under the field', async () => {
    let n = 0
    const stub = route({
      'GET /api/drive/folders': () => json(200, folders),
      'POST /api/drive/folder': () =>
        n++ === 0
          ? json(422, {
              error: 'A folder name is 1 to 100 characters, with no /.',
              field: 'name',
              code: 'invalid',
            })
          : json(200, status({ folder: { id: 'f3', name: 'new-one' } })),
    })
    renderCard(status())
    await screen.findByRole('option', { name: 'My Drive / archive' })
    fireEvent.change(screen.getByLabelText('Destination folder'), {
      target: { value: '__create__' },
    })
    const name = screen.getByLabelText('New folder name')
    expect(name).toHaveValue('server-backups')
    fireEvent.change(name, { target: { value: 'a/b' } })
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))
    expect(
      await screen.findByText('A folder name is 1 to 100 characters, with no /.'),
    ).toBeInTheDocument()
    fireEvent.change(name, { target: { value: 'new-one' } })
    expect(screen.queryByText('A folder name is 1 to 100 characters, with no /.')).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(screen.queryByLabelText('New folder name')).toBeNull())
    expect(posts(stub, '/api/drive/folder')).toEqual([{ name: 'a/b' }, { name: 'new-one' }])
    expect(screen.getByLabelText('Destination folder')).toHaveValue('f3')
  })

  it('with no folder chosen and none to choose from, the name field is already open', async () => {
    route({ 'GET /api/drive/folders': () => json(200, []) })
    renderCard(status({ folder: null }))
    expect(await screen.findByLabelText('New folder name')).toHaveValue('server-backups')
  })

  it('Test connection is disabled with a spinner while it runs, then says it worked', async () => {
    let finish: (r: Response) => void = () => {}
    const pending = new Promise<Response>((res) => {
      finish = res
    })
    route({
      'GET /api/drive/folders': () => json(200, folders),
      'POST /api/drive/test': () => pending as unknown as Response,
    })
    renderCard(status())
    const button = screen.getByRole('button', { name: 'Test connection' })
    fireEvent.click(button)
    expect(button).toBeDisabled()
    finish(json(200, { ok: true }))
    expect(await screen.findByRole('status')).toHaveTextContent(
      'Uploaded a 1 KB test file to Drive and deleted it',
    )
    expect(button).toBeEnabled()
  })

  it('a failed test shows the words and the code in red', async () => {
    route({
      'GET /api/drive/folders': () => json(200, folders),
      'POST /api/drive/test': () =>
        json(200, {
          ok: false,
          code: 'folder_missing',
          message: 'The destination folder is missing or in the trash. Choose or create a folder.',
        }),
    })
    renderCard(status())
    fireEvent.click(screen.getByRole('button', { name: 'Test connection' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('The destination folder is missing or in the trash.')
    expect(alert).toHaveTextContent('folder_missing')
  })

  it('a test already running elsewhere (409 busy) shows its words', async () => {
    route({
      'GET /api/drive/folders': () => json(200, folders),
      'POST /api/drive/test': () =>
        json(409, { error: 'busy', message: 'A connection test is already running.' }),
    })
    renderCard(status())
    fireEvent.click(screen.getByRole('button', { name: 'Test connection' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'A connection test is already running.',
    )
  })

  it('Disconnect asks first; Cancel posts nothing, Disconnect posts and shows Connect', async () => {
    const stub = route({
      'GET /api/drive/folders': () => json(200, folders),
      'POST /api/drive/disconnect': () =>
        json(200, status({ state: 'not_connected', email: '', folder: null })),
    })
    renderCard(status())
    fireEvent.click(screen.getByRole('button', { name: 'Disconnect' }))
    const dialog = screen.getByRole('dialog', { name: 'Disconnect Google Drive?' })
    expect(dialog).toHaveTextContent(
      'Backups stop until you connect again. Files already on Drive stay there.',
    )
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(posts(stub, '/api/drive/disconnect')).toHaveLength(0)

    fireEvent.click(screen.getByRole('button', { name: 'Disconnect' }))
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Disconnect' }))
    expect(await screen.findByRole('button', { name: 'Connect Google Drive' })).toBeInTheDocument()
    expect(posts(stub, '/api/drive/disconnect')).toHaveLength(1)
  })
})

describe('coming back from Google', () => {
  it('?drive=connected toasts the account and clears the query', async () => {
    route({ 'GET /api/drive/folders': () => json(200, folders) })
    renderCard(status(), '/settings?drive=connected')
    expect(await screen.findByTestId('toast-success')).toHaveTextContent(
      'Connected Google Drive as ops.backup@gmail.com',
    )
    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent(/^\/settings$/))
  })

  it('?drive=declined toasts that it was not connected', async () => {
    route({})
    renderCard(
      status({ state: 'not_connected', email: '', folder: null }),
      '/settings?drive=declined',
    )
    expect(await screen.findByRole('alert')).toHaveTextContent('Google Drive was not connected.')
    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent(/^\/settings$/))
  })

  it('?drive=error shows the code words and the code, and keeps other parameters', async () => {
    route({})
    renderCard(
      status({ state: 'not_connected', email: '', folder: null }),
      '/settings?drive=error&code=unreachable&x=1',
    )
    const toast = await screen.findByRole('alert')
    expect(toast).toHaveTextContent('Google Drive was not connected.')
    expect(toast).toHaveTextContent('Google Drive is unreachable.')
    expect(toast).toHaveTextContent('unreachable')
    await waitFor(() => expect(screen.getByTestId('where')).toHaveTextContent(/^\/settings\?x=1$/))
  })
})
