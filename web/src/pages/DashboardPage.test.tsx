import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { useLocation } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Project, Workspace } from '../api/types.gen'
import { formatLastScan } from '../lib/time'
import { FakeEventSource } from '../test/fakeEventSource'
import { makeBackup, makeDatabase } from '../test/projects'
import { renderApp } from '../test/renderApp'
import DashboardPage from './DashboardPage'

const projects: Project[] = [
  {
    id: 1,
    displayName: 'shop',
    path: '/srv/work/shop',
    workspaceId: 1,
    self: false,
    status: 'running',
    actions: ['stop', 'restart'],
    databases: [
      makeDatabase({ name: 'shop' }),
      makeDatabase({ type: 'sqlite', name: 'cache', locationKind: 'file', location: './cache.db' }),
    ],
    missingSince: '',
    publicDomain: '',
    stopTemplate: 'stop',
    alertCpuPercent: null,
    alertRamMb: null,
    composeFiles: ['compose.yaml'],
    cpuPercent: null,
    memBytes: null,
    upSince: '',
    containers: [],
    driveFolder: 'shop',
  },
  {
    id: 2,
    displayName: 'panel',
    path: '/srv/work/panel',
    workspaceId: 1,
    self: true,
    status: 'partial',
    actions: [],
    databases: [],
    missingSince: '',
    publicDomain: '',
    stopTemplate: 'stop',
    alertCpuPercent: null,
    alertRamMb: null,
    composeFiles: ['compose.yaml'],
    cpuPercent: null,
    memBytes: null,
    upSince: '',
    containers: [],
    driveFolder: 'panel',
  },
]

const workspaces: Workspace[] = [
  {
    id: 1,
    name: 'work',
    path: '/srv/work',
    depth: 3,
    lastScanAt: '2026-10-30T09:42:00Z',
    tracked: 2,
    ignored: 0,
  },
]

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status })

/**
 * serve stubs fetch by URL. GET /api/projects answers one entry per call, the
 * last one repeating; GET /api/workspaces answers the list given (one
 * workspace by default), so that load can never take a queued projects answer.
 */
function serve(...answers: Array<[number, unknown]>) {
  let n = 0
  const fetchStub = vi.fn(async (url: string) => {
    if (url === '/api/workspaces') return json(200, workspaces)
    const [status, body] = answers[Math.min(n++, answers.length - 1)]
    return json(status, body)
  })
  vi.stubGlobal('fetch', fetchStub)
  return fetchStub
}

/** gets lists the calls of one GET path. */
const gets = (stub: ReturnType<typeof vi.fn>, path: string) =>
  stub.mock.calls.filter(
    (c) => c[0] === path && (c[1] as RequestInit | undefined)?.method !== 'POST',
  )

/** Where shows the route it is mounted on, so a test can see where the page navigated. */
function Where() {
  const loc = useLocation()
  return (
    <div data-testid="where">
      {loc.pathname}
      <span data-testid="where-state">{JSON.stringify(loc.state)}</span>
    </div>
  )
}

const renderPage = () =>
  renderApp('/', {
    routes: [
      { path: '/', element: <DashboardPage /> },
      { path: '*', element: <Where /> },
    ],
  })

beforeEach(() => {
  sessionStorage.clear()
  FakeEventSource.instances = []
  vi.stubGlobal('EventSource', FakeEventSource)
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('DashboardPage', () => {
  it('shows the heading and a loading state before the answer', () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => new Promise(() => {})),
    )
    renderPage()
    expect(screen.getByRole('heading', { level: 1, name: 'Dashboard' })).toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent('Loading projects')
  })

  it('lists each tracked project with name, path, status and databases', async () => {
    const fetchStub = serve([200, projects])
    renderPage()

    const shop = await screen.findByRole('row', { name: /shop/ })
    expect(gets(fetchStub, '/api/projects')).toHaveLength(1)
    expect(within(shop).getByText('shop')).toBeInTheDocument()
    expect(within(shop).getByText('/srv/work/shop')).toBeInTheDocument()
    expect(within(shop).getByText('Running')).toBeInTheDocument()
    expect(within(shop).getByText('PostgreSQL · container db')).toBeInTheDocument()
    expect(within(shop).getByText('SQLite · file')).toBeInTheDocument()
    expect(within(shop).queryByText('Monitor only')).toBeNull()

    const panel = screen.getByRole('row', { name: /panel/ })
    expect(within(panel).getByText('Monitor only')).toBeInTheDocument()
    expect(within(panel).getByText('/srv/work/panel')).toBeInTheDocument()
    expect(within(panel).getByText('Partial')).toBeInTheDocument()
    expect(within(panel).getByText('None detected')).toBeInTheDocument()

    expect(screen.getAllByRole('columnheader').map((h) => h.textContent?.trim())).toEqual([
      'Display name',
      'Path',
      'Status',
      'CPU',
      'RAM',
      'Database',
      'Start / Stop',
    ])
  })

  it('links the name to the detail page', async () => {
    serve([200, projects])
    renderPage()
    const shop = await screen.findByRole('row', { name: /shop/ })
    expect(within(shop).getByRole('link', { name: 'shop' })).toHaveAttribute('href', '/projects/1')
    expect(
      within(screen.getByRole('row', { name: /panel/ })).getByRole('link', { name: 'panel' }),
    ).toHaveAttribute('href', '/projects/2')
  })

  it('shows CPU and RAM with bars, and dashes with no bar when null', async () => {
    const live: Project[] = [
      { ...projects[0], cpuPercent: 12.4, memBytes: 640 * 1024 * 1024 },
      projects[1],
    ]
    serve([200, live])
    renderPage()
    const shop = await screen.findByRole('row', { name: /shop/ })
    expect(within(shop).getByText('12%')).toBeInTheDocument()
    expect(within(shop).getByText('640 MB')).toBeInTheDocument()
    expect(within(shop).getByTestId('cpu-bar')).toHaveStyle({ width: '12.4%' })
    // 640 MB of the 4 GB range is 15.625 %.
    expect(within(shop).getByTestId('ram-bar')).toHaveStyle({ width: '15.625%' })

    const panel = screen.getByRole('row', { name: /panel/ })
    expect(within(panel).getAllByText('—')).toHaveLength(2)
    expect(within(panel).queryByTestId('cpu-bar')).toBeNull()
    expect(within(panel).queryByTestId('ram-bar')).toBeNull()
  })

  it('updates CPU and RAM from a live state without remounting the row', async () => {
    serve([200, projects])
    renderPage()
    const shop = await screen.findByRole('row', { name: /shop/ })
    expect(within(shop).queryByText('12%')).toBeNull()
    act(() => {
      const es = FakeEventSource.latest('/api/events')
      if (!es) throw new Error('no event source')
      es.open()
      es.emit('state', {
        dockerUp: true,
        projects: [{ ...projects[0], cpuPercent: 12, memBytes: 640 * 1024 * 1024 }, projects[1]],
      })
    })
    expect(screen.getByRole('row', { name: /shop/ })).toBe(shop)
    expect(within(shop).getByText('12%')).toBeInTheDocument()
  })

  it('shows docker compose down in the stop dialog for a down template', async () => {
    serve([200, [{ ...projects[0], stopTemplate: 'down' }]])
    renderPage()
    const shop = await screen.findByRole('row', { name: /shop/ })
    fireEvent.click(within(shop).getByRole('switch', { name: 'Stop shop' }))
    expect(screen.getByRole('dialog')).toHaveTextContent('$ docker compose down · timeout 60s')
  })

  it('restores the scroll position it saved on leaving', async () => {
    serve([200, projects])
    const scrollTo = vi.fn()
    vi.stubGlobal('scrollTo', scrollTo)
    Object.defineProperty(window, 'scrollY', { value: 340, configurable: true })
    const first = renderPage()
    await screen.findByRole('row', { name: /shop/ })
    first.unmount()
    expect(sessionStorage.getItem('dashboard.scrollY')).toBe('340')

    renderPage()
    await screen.findByRole('row', { name: /shop/ })
    expect(scrollTo).toHaveBeenCalledWith(0, 340)
    Object.defineProperty(window, 'scrollY', { value: 0, configurable: true })
  })

  it('shows the empty state with an Add workspace link to onboarding', async () => {
    serve([200, []])
    renderPage()
    expect(await screen.findByText('No projects yet')).toBeInTheDocument()
    expect(screen.queryByRole('table')).toBeNull()
    expect(screen.getByRole('link', { name: 'Add workspace' })).toHaveAttribute(
      'href',
      '/onboarding',
    )
  })

  it('shows an error with a retry that loads again', async () => {
    const fetchStub = serve([500, { error: 'docker is down' }], [200, projects])
    renderPage()

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Could not load projects')
    expect(alert).toHaveTextContent('docker is down')

    fireEvent.click(within(alert).getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('/srv/work/shop')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).toBeNull()
    expect(gets(fetchStub, '/api/projects')).toHaveLength(2)
  })

  it('treats a non-JSON 2xx as an error', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response('<html>login</html>', { status: 200 })),
    )
    renderPage()
    await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument())
    expect(screen.getByRole('alert')).toHaveTextContent('Unexpected response from the server.')
  })

  it('treats a JSON answer that is not a list as an error', async () => {
    serve([200, { email: 'someone@example.com' }])
    renderPage()
    expect(await screen.findByRole('alert')).toHaveTextContent('Unexpected response')
  })

  describe('lifecycle controls', () => {
    const stoppedShop: Project = {
      ...projects[0],
      id: 3,
      displayName: 'blog',
      path: '/srv/work/blog',
      status: 'stopped',
      actions: ['start'],
      databases: [],
    }
    const brokenShop: Project = {
      ...projects[0],
      id: 4,
      displayName: 'broken',
      path: '/srv/work/broken',
      status: 'error',
      actions: ['start', 'restart'],
      databases: [],
    }
    const list = [projects[0], projects[1], stoppedShop, brokenShop]

    /** serveActions answers GET /api/projects with the list and each POST with next(). */
    function serveActions(next: () => Promise<Response> | Response, rows: Project[] = list) {
      const fetchStub = vi.fn(async (url: string, init?: RequestInit) => {
        if (init?.method === 'POST') return next()
        if (url === '/api/workspaces') return json(200, workspaces)
        return new Response(JSON.stringify(rows), { status: 200 })
      })
      vi.stubGlobal('fetch', fetchStub)
      return fetchStub
    }
    const posts = (stub: ReturnType<typeof vi.fn>) =>
      stub.mock.calls.filter((c) => (c[1] as RequestInit | undefined)?.method === 'POST')
    const ok = (status: string, outcome = 'ok', message = '', actions: string[] = []) =>
      new Response(JSON.stringify({ outcome, status, actions, message }), { status: 200 })
    const row = (name: RegExp) => screen.getByRole('row', { name })

    it('confirms a stop, cancel sends nothing, confirm posts and shows Stopping then the toast', async () => {
      let release: (r: Response) => void = () => {}
      const stub = serveActions(() => new Promise<Response>((res) => (release = res)))
      renderPage()
      await screen.findByRole('row', { name: /shop/ })

      fireEvent.click(within(row(/shop/)).getByRole('switch', { name: 'Stop shop' }))
      const dialog = screen.getByRole('dialog')
      expect(within(dialog).getByRole('heading')).toHaveTextContent('Stop shop?')
      expect(dialog).toHaveTextContent(
        'It will stop serving until you start it again. Its volumes and data are kept.',
      )
      expect(dialog).toHaveTextContent('$ docker compose stop · timeout 60s')

      fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
      expect(screen.queryByRole('dialog')).toBeNull()
      expect(posts(stub)).toHaveLength(0)

      fireEvent.click(within(row(/shop/)).getByRole('switch', { name: 'Stop shop' }))
      fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Stop' }))
      expect(screen.queryByRole('dialog')).toBeNull()

      expect(posts(stub)).toHaveLength(1)
      const [url, init] = posts(stub)[0] as [string, RequestInit]
      expect(url).toBe('/api/projects/1/stop')
      expect(init.headers).toMatchObject({ 'Content-Type': 'application/json' })

      expect(within(row(/shop/)).getByText('Stopping…')).toBeInTheDocument()
      expect(within(row(/shop/)).getByRole('switch')).toBeDisabled()

      await act(async () => release(ok('stopped')))
      expect(await screen.findByTestId('toast-success')).toHaveTextContent('Stopped shop')
      expect(within(row(/shop/)).getByText('Stopped')).toBeInTheDocument()
    })

    it('starts a stopped row at once, with no dialog', async () => {
      const stub = serveActions(() => ok('running'))
      renderPage()
      await screen.findByRole('row', { name: /blog/ })

      fireEvent.click(within(row(/blog/)).getByRole('switch', { name: 'Start blog' }))
      expect(screen.queryByRole('dialog')).toBeNull()
      await waitFor(() => expect(posts(stub)).toHaveLength(1))
      expect((posts(stub)[0] as [string])[0]).toBe('/api/projects/3/start')
      expect(await screen.findByTestId('toast-success')).toHaveTextContent('Started blog')
    })

    it('restarts an error row at once and asks first for a running row', async () => {
      const stub = serveActions(() => ok('running'))
      renderPage()
      await screen.findByRole('row', { name: /broken/ })

      fireEvent.click(
        within(row(/broken/)).getByRole('button', { name: 'More actions for broken' }),
      )
      fireEvent.click(screen.getByRole('menuitem', { name: 'Restart' }))
      expect(screen.queryByRole('dialog')).toBeNull()
      await waitFor(() => expect(posts(stub)).toHaveLength(1))
      expect((posts(stub)[0] as [string])[0]).toBe('/api/projects/4/restart')

      fireEvent.click(within(row(/shop/)).getByRole('button', { name: 'More actions for shop' }))
      fireEvent.click(screen.getByRole('menuitem', { name: 'Restart' }))
      const dialog = screen.getByRole('dialog')
      expect(within(dialog).getByRole('heading')).toHaveTextContent('Restart shop?')
      expect(dialog).toHaveTextContent(
        'Its containers will stop and start again; it is unavailable for a moment. Volumes and data are kept.',
      )
      expect(dialog).toHaveTextContent('$ docker compose restart · timeout 60s')
      expect(posts(stub)).toHaveLength(1)
      fireEvent.click(within(dialog).getByRole('button', { name: 'Restart' }))
      await waitFor(() => expect(posts(stub)).toHaveLength(2))
      expect((posts(stub)[1] as [string])[0]).toBe('/api/projects/1/restart')
    })

    it('asks first before restarting a partial row too', async () => {
      const partialShop: Project = {
        ...projects[0],
        id: 5,
        displayName: 'notes',
        path: '/srv/work/notes',
        status: 'partial',
        actions: ['start', 'stop', 'restart'],
        databases: [],
      }
      const stub = serveActions(() => ok('running'), [partialShop])
      renderPage()
      await screen.findByRole('row', { name: /notes/ })
      expect(within(row(/notes/)).getByText('Partial')).toBeInTheDocument()

      fireEvent.click(within(row(/notes/)).getByRole('button', { name: 'More actions for notes' }))
      fireEvent.click(screen.getByRole('menuitem', { name: 'Restart' }))
      const dialog = screen.getByRole('dialog')
      expect(within(dialog).getByRole('heading')).toHaveTextContent('Restart notes?')
      expect(posts(stub)).toHaveLength(0)
      fireEvent.click(within(dialog).getByRole('button', { name: 'Restart' }))
      await waitFor(() => expect(posts(stub)).toHaveLength(1))
      expect((posts(stub)[0] as [string])[0]).toBe('/api/projects/5/restart')
    })

    it('shows a failed result as an alert toast that stays', async () => {
      serveActions(() => ok('running', 'failed', 'port 80 is in use'))
      renderPage()
      await screen.findByRole('row', { name: /shop/ })
      fireEvent.click(within(row(/shop/)).getByRole('switch', { name: 'Stop shop' }))
      fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Stop' }))

      const alert = await screen.findByRole('alert')
      expect(alert).toHaveTextContent("Couldn't stop shop")
      expect(alert).toHaveTextContent('port 80 is in use')
      expect(within(row(/shop/)).getByText('Running')).toBeInTheDocument()

      await act(async () => {
        await new Promise((r) => setTimeout(r, 50))
      })
      expect(screen.getByRole('alert')).toBeInTheDocument()
    })

    it('keeps an error toast past 10 seconds', async () => {
      serveActions(() => ok('running', 'timed_out', 'timed out after 60s'))
      renderPage()
      await screen.findByRole('row', { name: /shop/ })
      vi.useFakeTimers({ shouldAdvanceTime: true })
      fireEvent.click(within(row(/shop/)).getByRole('switch', { name: 'Stop shop' }))
      fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Stop' }))
      await screen.findByRole('alert')
      await act(async () => {
        vi.advanceTimersByTime(10_000)
      })
      expect(screen.getByRole('alert')).toHaveTextContent('timed out after 60s')
    })

    it('shows the server text of a 409 locked refusal', async () => {
      serveActions(
        () =>
          new Response(
            JSON.stringify({
              error: 'shop is busy with another command',
              code: 'locked',
              status: 'stopping',
            }),
            { status: 409 },
          ),
      )
      renderPage()
      await screen.findByRole('row', { name: /blog/ })
      fireEvent.click(within(row(/blog/)).getByRole('switch', { name: 'Start blog' }))
      const alert = await screen.findByRole('alert')
      expect(alert).toHaveTextContent("Couldn't start blog")
      expect(alert).toHaveTextContent('shop is busy with another command')
      // The row goes back to what the server last said.
      expect(within(row(/blog/)).getByText('Stopped')).toBeInTheDocument()
    })

    it('shows a network failure as an error toast', async () => {
      const stub = vi.fn(async (url: string, init?: RequestInit) => {
        if (init?.method === 'POST') throw new TypeError('Failed to fetch')
        if (url === '/api/workspaces') return json(200, workspaces)
        return new Response(JSON.stringify(list), { status: 200 })
      })
      vi.stubGlobal('fetch', stub)
      renderPage()
      await screen.findByRole('row', { name: /blog/ })
      fireEvent.click(within(row(/blog/)).getByRole('switch', { name: 'Start blog' }))
      expect(await screen.findByRole('alert')).toHaveTextContent('Failed to fetch')
    })

    it('follows a state event: stopping updates the row and locks the controls', async () => {
      serveActions(() => ok('stopped'))
      renderPage()
      await screen.findByRole('row', { name: /shop/ })

      act(() => {
        FakeEventSource.instances[0].emit('state', {
          dockerUp: true,
          projects: [{ ...projects[0], status: 'stopping', actions: [] }, projects[1]],
        })
      })
      const shop = row(/shop/)
      expect(within(shop).getByText('Stopping…')).toBeInTheDocument()
      expect(within(shop).getByRole('switch')).toBeDisabled()
      fireEvent.click(within(shop).getByRole('button', { name: 'More actions for shop' }))
      for (const name of ['Start', 'Restart']) {
        expect(screen.getByRole('menuitem', { name })).toHaveAttribute('aria-disabled', 'true')
      }
    })

    it('locks the controls of the panel itself with the reason Monitor only', async () => {
      serveActions(() => ok('running'))
      renderPage()
      await screen.findByRole('row', { name: /panel/ })
      const panel = row(/panel/)
      const sw = within(panel).getByRole('switch')
      expect(sw).toBeDisabled()
      expect(sw).toHaveAttribute('title', 'Monitor only')
      fireEvent.click(within(panel).getByRole('button', { name: 'More actions for panel' }))
      for (const name of ['Start', 'Restart']) {
        const item = screen.getByRole('menuitem', { name })
        expect(item).toHaveAttribute('aria-disabled', 'true')
        expect(item).toHaveAttribute('title', 'Monitor only')
      }
    })

    it('gives Docker down as the reason when the hub says so', async () => {
      serveActions(() => ok('running'))
      renderPage()
      await screen.findByRole('row', { name: /shop/ })
      act(() => {
        FakeEventSource.instances[0].emit('state', {
          dockerUp: false,
          projects: [{ ...projects[0], status: 'unknown', actions: [] }],
        })
      })
      expect(within(row(/shop/)).getByRole('switch')).toHaveAttribute(
        'title',
        'Docker is not responding',
      )
    })

    it('drops the pending status on the next state event, or after 5 seconds', async () => {
      serveActions(() => ok('stopped'))
      renderPage()
      await screen.findByRole('row', { name: /shop/ })

      // Next state event.
      fireEvent.click(within(row(/shop/)).getByRole('switch', { name: 'Stop shop' }))
      fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Stop' }))
      expect(await within(row(/shop/)).findByText('Stopped')).toBeInTheDocument()
      act(() => {
        FakeEventSource.instances[0].emit('state', {
          dockerUp: true,
          projects: [{ ...projects[0], status: 'partial', actions: ['start', 'stop', 'restart'] }],
        })
      })
      expect(within(row(/shop/)).getByText('Partial')).toBeInTheDocument()

      // After 5 s with no event, the row falls back to the list it has.
      vi.useFakeTimers({ shouldAdvanceTime: true })
      fireEvent.click(within(row(/shop/)).getByRole('switch', { name: 'Stop shop' }))
      fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Stop' }))
      await waitFor(() => expect(within(row(/shop/)).getByText('Stopped')).toBeInTheDocument())
      await act(async () => {
        vi.advanceTimersByTime(4900)
      })
      expect(within(row(/shop/)).getByText('Stopped')).toBeInTheDocument()
      await act(async () => {
        vi.advanceTimersByTime(200)
      })
      expect(within(row(/shop/)).getByText('Partial')).toBeInTheDocument()
    })

    it('does nothing harmful when the answer arrives after the page is gone', async () => {
      let release: (r: Response) => void = () => {}
      serveActions(() => new Promise<Response>((res) => (release = res)))
      const spy = vi.spyOn(console, 'error').mockImplementation(() => {})
      const { unmount } = renderPage()
      await screen.findByRole('row', { name: /blog/ })
      vi.useFakeTimers({ shouldAdvanceTime: true })
      fireEvent.click(within(row(/blog/)).getByRole('switch', { name: 'Start blog' }))
      unmount()
      const before = vi.getTimerCount()
      // A failed result shows an error toast, which holds no timer, so any
      // timer left is the page's own 5 s expiry.
      await act(async () => release(ok('stopped', 'failed', 'boom', ['start'])))
      expect(vi.getTimerCount()).toBe(before)
      expect(spy).not.toHaveBeenCalled()
      spy.mockRestore()
    })

    it('uses the settled status and actions the server sent, and none while running', async () => {
      let release: (r: Response) => void = () => {}
      serveActions(() => new Promise<Response>((res) => (release = res)))
      renderPage()
      await screen.findByRole('row', { name: /shop/ })
      fireEvent.click(within(row(/shop/)).getByRole('switch', { name: 'Stop shop' }))
      fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Stop' }))
      fireEvent.click(within(row(/shop/)).getByRole('button', { name: 'More actions for shop' }))
      for (const name of ['Start', 'Restart']) {
        expect(screen.getByRole('menuitem', { name })).toHaveAttribute('aria-disabled', 'true')
      }
      fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' })

      await act(async () => release(ok('stopped', 'ok', '', ['start'])))
      const sw = within(row(/shop/)).getByRole('switch', { name: 'Start shop' })
      expect(sw).toBeEnabled()
      expect(sw).not.toHaveAttribute('title')
    })
  })
  describe('workspaces, rescan and stop tracking', () => {
    type Handler = () => Response | Promise<Response>
    /** gone is a Not found project: its folder disappeared. */
    const gone: Project = {
      ...projects[0],
      id: 6,
      displayName: 'old-api',
      path: '/srv/work/old-api',
      status: 'notfound',
      actions: ['remove'],
      databases: [],
      missingSince: '2026-10-30T09:42:00Z',
    }
    /** route answers by "METHOD path"; anything else is a 404. */
    function route(handlers: Record<string, Handler>) {
      const stub = vi.fn(async (url: string, init?: RequestInit) => {
        const h = handlers[`${init?.method ?? 'GET'} ${url}`]
        return h ? h() : json(404, { error: 'not found' })
      })
      vi.stubGlobal('fetch', stub)
      return stub
    }
    const posts = (stub: ReturnType<typeof vi.fn>, url?: string) =>
      stub.mock.calls.filter(
        (c) => (c[1] as RequestInit | undefined)?.method === 'POST' && (!url || c[0] === url),
      )
    const header = () => screen.getByRole('heading', { level: 1 }).parentElement as HTMLElement
    const row = (name: RegExp) => screen.getByRole('row', { name })
    const where = () => screen.getByTestId('where').firstChild?.textContent

    describe('first screen', () => {
      it('goes to onboarding, replacing the entry, when there is no workspace', async () => {
        route({
          'GET /api/workspaces': () => json(200, []),
          'GET /api/projects': () => json(200, []),
        })
        renderPage()
        expect(await screen.findByTestId('where')).toHaveTextContent('/onboarding')
        expect(screen.queryByRole('heading', { name: 'Dashboard' })).toBeNull()
      })

      it('stays on the Dashboard with a workspace', async () => {
        const stub = route({
          'GET /api/workspaces': () => json(200, workspaces),
          'GET /api/projects': () => json(200, projects),
        })
        renderPage()
        await screen.findByRole('row', { name: /shop/ })
        expect(screen.queryByTestId('where')).toBeNull()
        expect(gets(stub, '/api/workspaces')).toHaveLength(1)
      })

      it('stays on the Dashboard when the workspaces fail to load, with no extra alert', async () => {
        route({
          'GET /api/workspaces': () => json(500, { error: 'db locked' }),
          'GET /api/projects': () => json(200, projects),
        })
        renderPage()
        await screen.findByRole('row', { name: /shop/ })
        expect(screen.queryByTestId('where')).toBeNull()
        expect(screen.queryByRole('alert')).toBeNull()
        expect(screen.queryByRole('button', { name: 'Rescan' })).toBeNull()
        expect(header()).toHaveTextContent('Tracked projects and their status')
      })

      it('keeps the subtitle and no Rescan button while the workspaces load', async () => {
        route({
          'GET /api/workspaces': () => new Promise<Response>(() => {}),
          'GET /api/projects': () => json(200, projects),
        })
        renderPage()
        await screen.findByRole('row', { name: /shop/ })
        expect(header()).toHaveTextContent('Tracked projects and their status')
        expect(screen.queryByRole('button', { name: 'Rescan' })).toBeNull()
      })
    })

    describe('header', () => {
      it('names the one workspace and when it was last scanned', async () => {
        route({
          'GET /api/workspaces': () => json(200, workspaces),
          'GET /api/projects': () => json(200, projects),
        })
        renderPage()
        await screen.findByRole('button', { name: 'Rescan' })
        const when = formatLastScan(workspaces[0].lastScanAt)
        expect(when).not.toBe('never')
        expect(header()).toHaveTextContent(`Workspace /srv/work · last scanned ${when}`)
        expect(screen.getByText('/srv/work')).toHaveClass('font-mono')
        expect(header()).not.toHaveTextContent('Tracked projects and their status')
      })

      it('counts several workspaces and uses the latest scan', async () => {
        const several: Workspace[] = [
          { ...workspaces[0], id: 1, lastScanAt: '2026-10-29T08:00:00Z' },
          { ...workspaces[0], id: 2, path: '/srv/more', lastScanAt: '2026-10-30T11:30:00Z' },
          { ...workspaces[0], id: 3, path: '/srv/old', lastScanAt: '' },
        ]
        route({
          'GET /api/workspaces': () => json(200, several),
          'GET /api/projects': () => json(200, projects),
        })
        renderPage()
        await screen.findByRole('button', { name: 'Rescan' })
        expect(header()).toHaveTextContent(
          `3 workspaces · last scanned ${formatLastScan('2026-10-30T11:30:00Z')}`,
        )
      })
    })

    describe('rescan', () => {
      const rescanRoutes = (rescan: Handler) => ({
        'GET /api/workspaces': () => json(200, workspaces),
        'GET /api/projects': () => json(200, projects),
        'POST /api/rescans': rescan,
      })

      it('posts for every workspace, with JSON, then opens the rescan page', async () => {
        const stub = route(rescanRoutes(() => json(202, { id: 7 })))
        renderPage()
        fireEvent.click(await screen.findByRole('button', { name: 'Rescan' }))
        expect(await screen.findByTestId('where')).toHaveTextContent('/rescan/7')
        const [url, init] = posts(stub)[0] as [string, RequestInit]
        expect(url).toBe('/api/rescans')
        expect(init.headers).toMatchObject({ 'Content-Type': 'application/json' })
        expect(JSON.parse(init.body as string)).toEqual({ workspaceId: 0 })
      })

      it('attaches to a running rescan on a 409', async () => {
        route(
          rescanRoutes(() =>
            json(409, {
              error: 'a scan is running',
              id: 9,
              path: 'all workspaces',
              depth: 0,
              rescan: true,
            }),
          ),
        )
        renderPage()
        fireEvent.click(await screen.findByRole('button', { name: 'Rescan' }))
        expect(await screen.findByTestId('where')).toHaveTextContent('/rescan/9')
      })

      it('goes to onboarding with the job id when a first scan runs', async () => {
        route(
          rescanRoutes(() =>
            json(409, {
              error: 'a scan is running',
              id: 4,
              path: '/srv/work',
              depth: 2,
              rescan: false,
            }),
          ),
        )
        renderPage()
        fireEvent.click(await screen.findByRole('button', { name: 'Rescan' }))
        await screen.findByTestId('where')
        expect(where()).toBe('/onboarding')
        expect(JSON.parse(screen.getByTestId('where-state').textContent ?? '')).toEqual({
          scanId: 4,
        })
      })

      it('shows an error toast and stays when the rescan cannot start', async () => {
        route(rescanRoutes(() => json(500, { error: 'disk full' })))
        renderPage()
        fireEvent.click(await screen.findByRole('button', { name: 'Rescan' }))
        const alert = await screen.findByRole('alert')
        expect(alert).toHaveTextContent('Could not start the rescan')
        expect(alert).toHaveTextContent('disk full')
        expect(screen.queryByTestId('where')).toBeNull()
        expect(screen.getByRole('button', { name: 'Rescan' })).toBeEnabled()
      })
    })

    describe('a Not found row', () => {
      it('says since when, and locks the switch', async () => {
        route({
          'GET /api/workspaces': () => json(200, workspaces),
          'GET /api/projects': () => json(200, [projects[0], gone]),
        })
        renderPage()
        const r = await screen.findByRole('row', { name: /old-api/ })
        expect(within(r).getByText('Not found')).toBeInTheDocument()
        expect(
          within(r).getByText(
            `Folder missing since the ${formatLastScan(gone.missingSince)} scan. Not removed automatically.`,
          ),
        ).toBeInTheDocument()
        const sw = within(r).getByRole('switch')
        expect(sw).toBeDisabled()
        expect(sw).toHaveAttribute('title', 'Not allowed while Not found')
        // A found row carries no sentence.
        expect(within(row(/shop/)).queryByText(/Folder missing/)).toBeNull()
      })
    })

    describe('stop tracking and remove from list', () => {
      const tracked: Project = { ...projects[0], actions: ['stop', 'restart', 'untrack'] }
      const list = [tracked, projects[1], gone]

      const setup = (untrack: Handler = () => json(200, { removed: true })) =>
        route({
          'GET /api/workspaces': () => json(200, workspaces),
          'GET /api/projects': () => json(200, list),
          'POST /api/projects/1/untrack': untrack,
          'POST /api/projects/6/untrack': untrack,
        })
      const openMenu = (name: RegExp, label: string) => {
        fireEvent.click(
          within(row(name)).getByRole('button', { name: `More actions for ${label}` }),
        )
      }

      it('asks first, cancel sends nothing, confirm posts JSON, toasts and hides the row', async () => {
        const stub = setup()
        renderPage()
        await screen.findByRole('row', { name: /shop/ })

        openMenu(/shop/, 'shop')
        fireEvent.click(screen.getByRole('menuitem', { name: 'Stop tracking' }))
        const dialog = screen.getByRole('dialog')
        expect(within(dialog).getByRole('heading')).toHaveTextContent('Stop tracking shop?')
        expect(dialog).toHaveTextContent(
          'Removes shop from the panel. Its containers, files and backups on Google Drive are not touched.',
        )
        expect(dialog).not.toHaveTextContent('timeout')
        fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
        expect(screen.queryByRole('dialog')).toBeNull()
        expect(posts(stub)).toHaveLength(0)

        openMenu(/shop/, 'shop')
        fireEvent.click(screen.getByRole('menuitem', { name: 'Stop tracking' }))
        const confirm = within(screen.getByRole('dialog')).getByRole('button', {
          name: 'Stop tracking',
        })
        expect(confirm).toHaveClass('bg-stop')
        fireEvent.click(confirm)

        expect(await screen.findByTestId('toast-success')).toHaveTextContent(
          'Stopped tracking shop',
        )
        expect(posts(stub)).toHaveLength(1)
        const [url, init] = posts(stub)[0] as [string, RequestInit]
        expect(url).toBe('/api/projects/1/untrack')
        expect(init.headers).toMatchObject({ 'Content-Type': 'application/json' })
        expect(screen.queryByRole('row', { name: /shop/ })).toBeNull()
        expect(screen.getByRole('row', { name: /panel/ })).toBeInTheDocument()
      })

      it('keeps the row gone when the next live state arrives without it', async () => {
        setup()
        renderPage()
        await screen.findByRole('row', { name: /shop/ })
        openMenu(/shop/, 'shop')
        fireEvent.click(screen.getByRole('menuitem', { name: 'Stop tracking' }))
        fireEvent.click(
          within(screen.getByRole('dialog')).getByRole('button', { name: 'Stop tracking' }),
        )
        await waitFor(() => expect(screen.queryByRole('row', { name: /shop/ })).toBeNull())

        // The hub's next state is the truth: without the project it stays gone.
        act(() => {
          FakeEventSource.instances[0].emit('state', { dockerUp: true, projects: [projects[1]] })
        })
        expect(screen.queryByRole('row', { name: /shop/ })).toBeNull()
        expect(screen.getByRole('row', { name: /panel/ })).toBeInTheDocument()
      })

      it('shows the row again when a live state brings the project back', async () => {
        setup()
        renderPage()
        await screen.findByRole('row', { name: /shop/ })
        openMenu(/shop/, 'shop')
        fireEvent.click(screen.getByRole('menuitem', { name: 'Stop tracking' }))
        fireEvent.click(
          within(screen.getByRole('dialog')).getByRole('button', { name: 'Stop tracking' }),
        )
        await waitFor(() => expect(screen.queryByRole('row', { name: /shop/ })).toBeNull())

        // A re-tracked project keeps its id; the overlay must not hide it for good.
        act(() => {
          FakeEventSource.instances[0].emit('state', {
            dockerUp: true,
            projects: [projects[0], projects[1]],
          })
        })
        expect(screen.getByRole('row', { name: /shop/ })).toBeInTheDocument()
      })

      it('keeps the row hidden when a live state still holds the project with no actions', async () => {
        setup()
        renderPage()
        await screen.findByRole('row', { name: /shop/ })
        openMenu(/shop/, 'shop')
        fireEvent.click(screen.getByRole('menuitem', { name: 'Stop tracking' }))
        fireEvent.click(
          within(screen.getByRole('dialog')).getByRole('button', { name: 'Stop tracking' }),
        )
        await waitFor(() => expect(screen.queryByRole('row', { name: /shop/ })).toBeNull())

        // The store write is still out: the hub shows the project held, with no actions.
        act(() => {
          FakeEventSource.instances[0].emit('state', {
            dockerUp: true,
            projects: [{ ...projects[0], actions: [] }, projects[1]],
          })
        })
        expect(screen.queryByRole('row', { name: /shop/ })).toBeNull()

        // Once it is gone from the state, it stays gone; back with actions, it returns.
        act(() => {
          FakeEventSource.instances[0].emit('state', {
            dockerUp: true,
            projects: [projects[0], projects[1]],
          })
        })
        expect(screen.getByRole('row', { name: /shop/ })).toBeInTheDocument()
      })

      it('sends one request however often it is confirmed while it is out', async () => {
        let release: (r: Response) => void = () => {}
        const stub = setup(() => new Promise<Response>((res) => (release = res)))
        renderPage()
        await screen.findByRole('row', { name: /shop/ })
        for (let i = 0; i < 2; i++) {
          openMenu(/shop/, 'shop')
          fireEvent.click(screen.getByRole('menuitem', { name: 'Stop tracking' }))
          fireEvent.click(
            within(screen.getByRole('dialog')).getByRole('button', { name: 'Stop tracking' }),
          )
        }
        expect(posts(stub)).toHaveLength(1)
        await act(async () => release(json(200, { removed: true })))
        expect(await screen.findByTestId('toast-success')).toHaveTextContent(
          'Stopped tracking shop',
        )
      })

      it('confirms Remove from list for a Not found row with its own words', async () => {
        const stub = setup()
        renderPage()
        await screen.findByRole('row', { name: /old-api/ })

        openMenu(/old-api/, 'old-api')
        const item = screen.getByRole('menuitem', { name: 'Remove from list' })
        expect(item).toHaveClass('text-danger')
        fireEvent.click(item)
        const dialog = screen.getByRole('dialog')
        expect(within(dialog).getByRole('heading')).toHaveTextContent(
          'Remove old-api from the list?',
        )
        expect(dialog).toHaveTextContent(
          'Its folder is gone. It leaves the panel; nothing on the server is touched.',
        )
        expect(posts(stub)).toHaveLength(0)
        fireEvent.click(within(dialog).getByRole('button', { name: 'Remove from list' }))

        expect(await screen.findByTestId('toast-success')).toHaveTextContent('Removed old-api')
        expect((posts(stub)[0] as [string])[0]).toBe('/api/projects/6/untrack')
        expect(screen.queryByRole('row', { name: /old-api/ })).toBeNull()
      })

      it('shows the server text of a 409 in an error toast and keeps the row', async () => {
        setup(() => json(409, { error: 'shop is busy with another command', code: 'locked' }))
        renderPage()
        await screen.findByRole('row', { name: /shop/ })
        openMenu(/shop/, 'shop')
        fireEvent.click(screen.getByRole('menuitem', { name: 'Stop tracking' }))
        fireEvent.click(
          within(screen.getByRole('dialog')).getByRole('button', { name: 'Stop tracking' }),
        )

        const alert = await screen.findByRole('alert')
        expect(alert).toHaveTextContent("Couldn't stop tracking shop")
        expect(alert).toHaveTextContent('shop is busy with another command')
        expect(screen.queryByTestId('toast-success')).toBeNull()
        expect(screen.getByRole('row', { name: /shop/ })).toBeInTheDocument()
      })

      it('shows no red item for a row whose actions carry neither', async () => {
        setup()
        renderPage()
        await screen.findByRole('row', { name: /panel/ })
        openMenu(/panel/, 'panel')
        expect(screen.queryByRole('menuitem', { name: 'Stop tracking' })).toBeNull()
        expect(screen.queryByRole('menuitem', { name: 'Remove from list' })).toBeNull()
      })
    })
  })

  describe('back up now in the ⋯ menu', () => {
    const one: Project = {
      ...projects[0],
      databases: [
        makeDatabase({ name: 'shop', backup: { allowed: true, reason: '', service: '' } }),
      ],
    }
    const none: Project = {
      ...projects[0],
      id: 3,
      displayName: 'blog',
      path: '/srv/work/blog',
      databases: [],
    }

    function serveBackup() {
      const stub = vi.fn(async (url: string, init?: RequestInit) => {
        if (init?.method === 'POST' && url === '/api/projects/1/backups')
          return json(202, { id: 7 })
        if (url === '/api/backups/7') return json(200, makeBackup())
        if (url === '/api/workspaces') return json(200, workspaces)
        return json(200, [one, none])
      })
      vi.stubGlobal('fetch', stub)
      return stub
    }

    it('starts the backup from the menu and opens its progress', async () => {
      const stub = serveBackup()
      renderPage()
      const row = await screen.findByRole('row', { name: /shop/ })
      fireEvent.click(within(row).getByRole('button', { name: 'More actions for shop' }))
      const item = screen.getByRole('menuitem', { name: 'Back up now' })
      expect(item).not.toHaveAttribute('aria-disabled')
      fireEvent.click(item)
      expect(await screen.findByRole('dialog', { name: 'Backup shop · shop' })).toBeInTheDocument()
      const [, init] = stub.mock.calls.find((c) => c[0] === '/api/projects/1/backups') ?? []
      expect(JSON.parse(String((init as RequestInit).body))).toEqual({
        database: 'postgresql:db:shop',
      })
    })

    it('disables the item with its reason', async () => {
      const stub = serveBackup()
      renderPage()
      const row = await screen.findByRole('row', { name: /blog/ })
      fireEvent.click(within(row).getByRole('button', { name: 'More actions for blog' }))
      const item = screen.getByRole('menuitem', { name: 'Back up now' })
      expect(item).toHaveAttribute('aria-disabled', 'true')
      expect(item).toHaveAttribute('title', 'No databases detected in this project.')
      fireEvent.click(item)
      expect(screen.queryByRole('dialog')).toBeNull()
      expect(
        stub.mock.calls.some((c) => (c[1] as RequestInit | undefined)?.method === 'POST'),
      ).toBe(false)
    })
  })
})
