import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { useLocation } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Project, Workspace } from '../api/types.gen'
import { LatestBackupsProvider } from '../app/latestBackups'
import { formatLastScan } from '../lib/time'
import { FakeEventSource } from '../test/fakeEventSource'
import { makeBackup, makeDatabase } from '../test/projects'
import { renderApp } from '../test/renderApp'
import { makeSeries, makeServerInfo } from '../test/server'
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

type Handler = () => Response | Promise<Response>

/** DEFAULTS answers the reads every Dashboard makes besides its projects. */
const DEFAULTS: Record<string, Handler> = {
  'GET /api/workspaces': () => json(200, workspaces),
  'GET /api/server': () => json(200, makeServerInfo()),
  'GET /api/server/metrics?range=1h': () => json(200, makeSeries({ range: '1h' })),
  'GET /api/backups/latest': () => json(200, { latest: null, projects: [] }),
}

/**
 * route stubs fetch by "METHOD path" (a "POST *" handler takes every POST
 * the test did not name); the Dashboard's other reads fall back to DEFAULTS
 * and anything else is a 404.
 */
function route(handlers: Record<string, Handler>) {
  const stub = vi.fn(async (url: string, init?: RequestInit) => {
    const method = init?.method ?? 'GET'
    const h =
      handlers[`${method} ${url}`] ??
      (method === 'POST' ? handlers['POST *'] : undefined) ??
      DEFAULTS[`${method} ${url}`]
    return h ? h() : json(404, { error: `unrouted ${method} ${url}` })
  })
  vi.stubGlobal('fetch', stub)
  return stub
}

/**
 * serve answers GET /api/projects one entry per call, the last one
 * repeating; the other reads answer by path, so none of them can take a
 * queued projects answer.
 */
function serve(...answers: Array<[number, unknown]>) {
  let n = 0
  return route({
    'GET /api/projects': () => {
      const [status, body] = answers[Math.min(n++, answers.length - 1)]
      return json(status, body)
    },
  })
}

/** gets lists the calls of one GET path. */
const gets = (stub: ReturnType<typeof vi.fn>, path: string) =>
  stub.mock.calls.filter(
    (c) => c[0] === path && (c[1] as RequestInit | undefined)?.method !== 'POST',
  )

/** card is a project's card in the card view, found by its name. */
const card = (name: string) => screen.getByRole('article', { name })
const findCard = (name: string) => screen.findByRole('article', { name })
const queryCard = (name: string) => screen.queryByRole('article', { name })

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

const renderPage = (entry = '/', page = <DashboardPage />) =>
  renderApp(entry, {
    routes: [
      { path: '/', element: page },
      { path: '*', element: <Where /> },
    ],
  })

/** emit sends one live state on the page's /api/events source. */
function emit(state: unknown) {
  act(() => {
    const es = FakeEventSource.latest('/api/events')
    if (!es) throw new Error('no event source')
    if (es.readyState === FakeEventSource.CONNECTING) es.open()
    es.emit('state', state)
  })
}

beforeEach(() => {
  sessionStorage.clear()
  localStorage.clear()
  FakeEventSource.instances = []
  vi.stubGlobal('EventSource', FakeEventSource)
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('DashboardPage', () => {
  it('shows the heading and skeletons before the answer: six tiles, a bar and six cards', () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => new Promise(() => {})),
    )
    const { container } = renderPage()
    expect(screen.getByRole('heading', { level: 1, name: 'Dashboard' })).toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent('Loading projects…')
    expect(container.querySelectorAll('[data-skeleton-tile]')).toHaveLength(6)
    expect(container.querySelectorAll('[data-skeleton-bar]')).toHaveLength(1)
    expect(container.querySelectorAll('[data-skeleton-card]')).toHaveLength(6)
    expect(container.querySelectorAll('[data-skeleton-row]')).toHaveLength(0)
  })

  it('shows three row skeletons in the table view', () => {
    localStorage.setItem('dashboard.view', 'table')
    vi.stubGlobal(
      'fetch',
      vi.fn(() => new Promise(() => {})),
    )
    const { container } = renderPage()
    expect(screen.getByRole('status')).toHaveTextContent('Loading projects…')
    expect(container.querySelectorAll('[data-skeleton-row]')).toHaveLength(3)
    expect(container.querySelectorAll('[data-skeleton-card]')).toHaveLength(0)
  })

  it('composes the server summary, the quick counts, the filters and a card per project', async () => {
    const fetchStub = serve([200, projects])
    renderPage()

    const shop = await findCard('shop')
    expect(gets(fetchStub, '/api/projects')).toHaveLength(1)
    expect(within(shop).getByText('Running')).toBeInTheDocument()
    expect(within(shop).queryByText('Monitor only')).toBeNull()

    const panel = card('panel')
    expect(within(panel).getByText('Monitor only')).toBeInTheDocument()
    expect(within(panel).getByText('Partial')).toBeInTheDocument()

    expect(await screen.findByRole('article', { name: 'CPU' })).toBeInTheDocument()
    const counts = screen.getByRole('region', { name: 'Quick counts' })
    expect(counts).toHaveTextContent('1 running')
    expect(counts).toHaveTextContent('1 partial')
    expect(screen.getByRole('group', { name: 'Filter by status' })).toBeInTheDocument()
    expect(screen.getByRole('searchbox', { name: 'Search projects by name' })).toBeInTheDocument()
    expect(screen.queryByRole('table')).toBeNull()
  })

  it('links the name to the detail page', async () => {
    serve([200, projects])
    renderPage()
    const shop = await findCard('shop')
    expect(within(shop).getByRole('link', { name: 'shop' })).toHaveAttribute('href', '/projects/1')
    expect(within(card('panel')).getByRole('link', { name: 'panel' })).toHaveAttribute(
      'href',
      '/projects/2',
    )
  })

  it('updates CPU and RAM from a live state without remounting the card', async () => {
    serve([200, projects])
    renderPage()
    const shop = await findCard('shop')
    expect(within(shop).queryByText('12%')).toBeNull()
    emit({
      dockerUp: true,
      projects: [{ ...projects[0], cpuPercent: 12, memBytes: 640 * 1024 * 1024 }, projects[1]],
    })
    expect(card('shop')).toBe(shop)
    expect(within(shop).getByText('12%')).toBeInTheDocument()
    expect(within(shop).getByText('640 MB')).toBeInTheDocument()
  })

  it('changes the counts with a live state, with no reload', async () => {
    const fetchStub = serve([200, projects])
    renderPage()
    await findCard('shop')
    const counts = screen.getByRole('region', { name: 'Quick counts' })
    expect(counts).toHaveTextContent('1 running')
    expect(counts).toHaveTextContent('0 stopped')

    emit({
      dockerUp: true,
      projects: [{ ...projects[0], status: 'stopped', actions: ['start'] }, projects[1]],
    })
    expect(counts).toHaveTextContent('0 running')
    expect(counts).toHaveTextContent('1 stopped')
    expect(gets(fetchStub, '/api/projects')).toHaveLength(1)
  })

  it('shows each project its own last backup from the latest backups', async () => {
    route({
      'GET /api/projects': () => json(200, projects),
      'GET /api/backups/latest': () => {
        const b = {
          id: 9,
          projectId: 1,
          projectName: 'shop',
          database: 'shop',
          state: 'failed',
          at: '2026-10-31T02:00:00Z',
        }
        return json(200, { latest: b, projects: [b] })
      },
    })
    renderPage(
      '/',
      <LatestBackupsProvider>
        <DashboardPage />
      </LatestBackupsProvider>,
    )
    await findCard('shop')
    // Before the read answers there is nothing to say.
    expect(within(card('shop')).getByTestId('last-backup')).toHaveTextContent('—')
    emit({ dockerUp: true, projects })
    await waitFor(() =>
      expect(within(card('shop')).getByTestId('last-backup')).toHaveTextContent('· failed'),
    )
    expect(within(card('panel')).getByTestId('last-backup')).toHaveTextContent('No backups yet')
    expect(screen.getByRole('region', { name: 'Quick counts' })).toHaveTextContent('Latest backup:')
  })

  it('shows docker compose down in the stop dialog for a down template', async () => {
    serve([200, [{ ...projects[0], stopTemplate: 'down' }]])
    renderPage()
    const shop = await findCard('shop')
    fireEvent.click(within(shop).getByRole('switch', { name: 'Stop shop' }))
    expect(screen.getByRole('dialog')).toHaveTextContent('$ docker compose down · timeout 60s')
  })

  it('restores the scroll position it saved on leaving', async () => {
    serve([200, projects])
    const scrollTo = vi.fn()
    vi.stubGlobal('scrollTo', scrollTo)
    Object.defineProperty(window, 'scrollY', { value: 340, configurable: true })
    const first = renderPage()
    await findCard('shop')
    first.unmount()
    expect(sessionStorage.getItem('dashboard.scrollY')).toBe('340')

    renderPage()
    await findCard('shop')
    expect(scrollTo).toHaveBeenCalledWith(0, 340)
    Object.defineProperty(window, 'scrollY', { value: 0, configurable: true })
  })

  it('shows the empty state with an Add workspace link to onboarding, and no filter bar', async () => {
    serve([200, []])
    renderPage()
    expect(await screen.findByText('No projects yet')).toBeInTheDocument()
    expect(screen.queryByRole('table')).toBeNull()
    expect(screen.queryByRole('article', { name: 'shop' })).toBeNull()
    expect(screen.getByRole('link', { name: 'Add workspace' })).toHaveAttribute(
      'href',
      '/onboarding',
    )
    expect(screen.queryByRole('group', { name: 'Filter by status' })).toBeNull()
    expect(screen.queryByRole('searchbox')).toBeNull()
    expect(screen.queryByText('No projects match these filters.')).toBeNull()
  })

  it('shows an error with a retry that loads again', async () => {
    const fetchStub = serve([500, { error: 'docker is down' }], [200, projects])
    renderPage()

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Could not load projects')
    expect(alert).toHaveTextContent('docker is down')

    fireEvent.click(within(alert).getByRole('button', { name: 'Retry' }))
    expect(await findCard('shop')).toBeInTheDocument()
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

  describe('table view', () => {
    const row = (name: RegExp) => screen.getByRole('row', { name })

    it('shows the same projects with their controls, in the table columns', async () => {
      serve([200, [{ ...projects[0], publicDomain: 'shop.vn' }, projects[1]]])
      renderPage('/?view=table')
      const shop = await screen.findByRole('row', { name: /shop/ })
      expect(screen.queryByRole('article', { name: 'shop' })).toBeNull()
      expect(screen.getAllByRole('columnheader').map((h) => h.textContent?.trim())).toEqual([
        'Project',
        'Status',
        'Domain',
        'CPU',
        'RAM',
        'Uptime',
        'Last backup',
        'Start / Stop',
      ])
      expect(within(shop).getByRole('link', { name: 'shop' })).toHaveAttribute(
        'href',
        '/projects/1',
      )
      expect(within(shop).getByRole('link', { name: 'shop.vn' })).toHaveAttribute(
        'href',
        'https://shop.vn',
      )
      expect(within(shop).getByText('Running')).toBeInTheDocument()
      expect(within(shop).getByText('Compose')).toBeInTheDocument()
      expect(within(shop).getByRole('switch', { name: 'Stop shop' })).toBeEnabled()
      expect(
        within(shop).getByRole('button', { name: 'More actions for shop' }),
      ).toBeInTheDocument()
      expect(within(row(/panel/)).getByText('Monitor only')).toBeInTheDocument()
      expect(within(row(/panel/)).getByText('No public domain')).toBeInTheDocument()
    })

    it('runs a stop from a row, through the same confirm', async () => {
      const stub = route({
        'GET /api/projects': () => json(200, projects),
        'POST /api/projects/1/stop': () =>
          json(200, { outcome: 'ok', status: 'stopped', actions: ['start'], message: '' }),
      })
      renderPage('/?view=table')
      await screen.findByRole('row', { name: /shop/ })
      fireEvent.click(within(row(/shop/)).getByRole('switch', { name: 'Stop shop' }))
      fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Stop' }))
      expect(await screen.findByTestId('toast-success')).toHaveTextContent('Stopped shop')
      expect(stub.mock.calls.some((c) => c[0] === '/api/projects/1/stop')).toBe(true)
      expect(within(row(/shop/)).getByText('Stopped')).toBeInTheDocument()
    })
  })

  describe('filters and views in the URL', () => {
    const search = () => screen.getByRole('searchbox', { name: 'Search projects by name' })
    const pressed = (name: string) =>
      within(screen.getByRole('group', { name: 'Filter by status' }))
        .getByRole('button', { name: new RegExp(`^${name}`) })
        .getAttribute('aria-pressed')

    it('reads status and search from the URL and shows only the matching cards', async () => {
      serve([200, projects])
      renderPage('/?status=partial&q=PAN')
      await findCard('panel')
      expect(queryCard('shop')).toBeNull()
      expect(pressed('Partial')).toBe('true')
      expect(search()).toHaveValue('PAN')
    })

    it('writes a chip to the URL, and the search replacing the entry', async () => {
      serve([200, projects])
      const { router } = renderPage()
      await findCard('shop')

      fireEvent.click(screen.getByRole('button', { name: /^Running/ }))
      expect(router.state.location.search).toBe('?status=running')
      expect(router.state.historyAction).toBe('PUSH')
      expect(queryCard('panel')).toBeNull()

      fireEvent.change(search(), { target: { value: 'sh' } })
      expect(router.state.location.search).toBe('?status=running&q=sh')
      expect(router.state.historyAction).toBe('REPLACE')
      expect(card('shop')).toBeInTheDocument()

      fireEvent.click(screen.getByRole('button', { name: /^All/ }))
      fireEvent.change(search(), { target: { value: '' } })
      expect(router.state.location.search).toBe('')
      expect(card('panel')).toBeInTheDocument()
    })

    it('keeps status and search when switching to Table and back, and remembers the view', async () => {
      serve([200, projects])
      const { router } = renderPage('/?status=running&q=sh')
      await findCard('shop')

      fireEvent.click(screen.getByRole('button', { name: 'Table' }))
      const params = () => new URLSearchParams(router.state.location.search)
      expect(params().get('view')).toBe('table')
      expect(params().get('status')).toBe('running')
      expect(params().get('q')).toBe('sh')
      expect(localStorage.getItem('dashboard.view')).toBe('table')
      expect(screen.getByRole('row', { name: /shop/ })).toBeInTheDocument()
      expect(screen.queryByRole('row', { name: /panel/ })).toBeNull()

      fireEvent.click(screen.getByRole('button', { name: 'Cards' }))
      expect(params().get('view')).toBe('cards')
      expect(params().get('status')).toBe('running')
      expect(params().get('q')).toBe('sh')
      expect(localStorage.getItem('dashboard.view')).toBe('cards')
      expect(card('shop')).toBeInTheDocument()
    })

    it('opens the remembered view when the URL names none', async () => {
      localStorage.setItem('dashboard.view', 'table')
      serve([200, projects])
      renderPage()
      expect(await screen.findByRole('row', { name: /shop/ })).toBeInTheDocument()
      expect(screen.getByRole('button', { name: 'Table' })).toHaveAttribute('aria-pressed', 'true')
    })

    it('lets the URL view win over the remembered one', async () => {
      localStorage.setItem('dashboard.view', 'table')
      serve([200, projects])
      renderPage('/?view=cards')
      expect(await findCard('shop')).toBeInTheDocument()
      expect(screen.queryByRole('table')).toBeNull()
    })

    it('says nothing matches, and Clear filters resets status and search but not the view', async () => {
      serve([200, projects])
      const { router } = renderPage('/?status=error&q=zzz&view=table')
      expect(await screen.findByText('No projects match these filters.')).toBeInTheDocument()
      expect(screen.queryByRole('table')).toBeNull()
      // The bar stays, so the filters can be changed.
      expect(search()).toHaveValue('zzz')

      fireEvent.click(screen.getByRole('button', { name: 'Clear filters' }))
      expect(router.state.location.search).toBe('?view=table')
      expect(screen.queryByText('No projects match these filters.')).toBeNull()
      expect(screen.getByRole('row', { name: /shop/ })).toBeInTheDocument()
      expect(screen.getByRole('row', { name: /panel/ })).toBeInTheDocument()
      expect(search()).toHaveValue('')
    })

    it('counts every row under the chips whatever is filtered', async () => {
      serve([200, projects])
      renderPage('/?q=shop')
      await findCard('shop')
      expect(screen.getByRole('button', { name: /^All/ })).toHaveTextContent('All2')
      expect(screen.getByRole('button', { name: /^Partial/ })).toHaveTextContent('Partial1')
    })

    it('keeps an in-flight card under its chip until it settles', async () => {
      let release: (r: Response) => void = () => {}
      route({
        'GET /api/projects': () => json(200, projects),
        'POST /api/projects/1/stop': () => new Promise<Response>((res) => (release = res)),
      })
      renderPage('/?status=running')
      await findCard('shop')
      fireEvent.click(within(card('shop')).getByRole('switch', { name: 'Stop shop' }))
      fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Stop' }))

      // The overlay's Stopping… row is still one of the rows the filter sees.
      expect(within(card('shop')).getByText('Stopping…')).toBeInTheDocument()
      expect(screen.getByRole('region', { name: 'Quick counts' })).toHaveTextContent('0 running')

      await act(async () =>
        release(json(200, { outcome: 'ok', status: 'stopped', actions: ['start'], message: '' })),
      )
      await waitFor(() => expect(queryCard('shop')).toBeNull())
      expect(screen.getByText('No projects match these filters.')).toBeInTheDocument()
    })
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
      return route({ 'GET /api/projects': () => json(200, rows), 'POST *': next })
    }
    const posts = (stub: ReturnType<typeof vi.fn>) =>
      stub.mock.calls.filter((c) => (c[1] as RequestInit | undefined)?.method === 'POST')
    const ok = (status: string, outcome = 'ok', message = '', actions: string[] = []) =>
      new Response(JSON.stringify({ outcome, status, actions, message }), { status: 200 })

    it('confirms a stop, cancel sends nothing, confirm posts and shows Stopping then the toast', async () => {
      let release: (r: Response) => void = () => {}
      const stub = serveActions(() => new Promise<Response>((res) => (release = res)))
      renderPage()
      await findCard('shop')

      fireEvent.click(within(card('shop')).getByRole('switch', { name: 'Stop shop' }))
      const dialog = screen.getByRole('dialog')
      expect(within(dialog).getByRole('heading')).toHaveTextContent('Stop shop?')
      expect(dialog).toHaveTextContent(
        'It will stop serving until you start it again. Its volumes and data are kept.',
      )
      expect(dialog).toHaveTextContent('$ docker compose stop · timeout 60s')

      fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
      expect(screen.queryByRole('dialog')).toBeNull()
      expect(posts(stub)).toHaveLength(0)

      fireEvent.click(within(card('shop')).getByRole('switch', { name: 'Stop shop' }))
      fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Stop' }))
      expect(screen.queryByRole('dialog')).toBeNull()

      expect(posts(stub)).toHaveLength(1)
      const [url, init] = posts(stub)[0] as [string, RequestInit]
      expect(url).toBe('/api/projects/1/stop')
      expect(init.headers).toMatchObject({ 'Content-Type': 'application/json' })

      expect(within(card('shop')).getByText('Stopping…')).toBeInTheDocument()
      expect(within(card('shop')).getByRole('switch')).toBeDisabled()

      await act(async () => release(ok('stopped')))
      expect(await screen.findByTestId('toast-success')).toHaveTextContent('Stopped shop')
      expect(within(card('shop')).getByText('Stopped')).toBeInTheDocument()
    })

    it('starts a stopped row at once, with no dialog', async () => {
      const stub = serveActions(() => ok('running'))
      renderPage()
      await findCard('blog')

      fireEvent.click(within(card('blog')).getByRole('switch', { name: 'Start blog' }))
      expect(screen.queryByRole('dialog')).toBeNull()
      await waitFor(() => expect(posts(stub)).toHaveLength(1))
      expect((posts(stub)[0] as [string])[0]).toBe('/api/projects/3/start')
      expect(await screen.findByTestId('toast-success')).toHaveTextContent('Started blog')
    })

    it('restarts an error row at once and asks first for a running row', async () => {
      const stub = serveActions(() => ok('running'))
      renderPage()
      await findCard('broken')

      fireEvent.click(
        within(card('broken')).getByRole('button', { name: 'More actions for broken' }),
      )
      fireEvent.click(screen.getByRole('menuitem', { name: 'Restart' }))
      expect(screen.queryByRole('dialog')).toBeNull()
      await waitFor(() => expect(posts(stub)).toHaveLength(1))
      expect((posts(stub)[0] as [string])[0]).toBe('/api/projects/4/restart')

      fireEvent.click(within(card('shop')).getByRole('button', { name: 'More actions for shop' }))
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
      await findCard('notes')
      expect(within(card('notes')).getByText('Partial')).toBeInTheDocument()

      fireEvent.click(within(card('notes')).getByRole('button', { name: 'More actions for notes' }))
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
      await findCard('shop')
      fireEvent.click(within(card('shop')).getByRole('switch', { name: 'Stop shop' }))
      fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Stop' }))

      const alert = await screen.findByRole('alert')
      expect(alert).toHaveTextContent("Couldn't stop shop")
      expect(alert).toHaveTextContent('port 80 is in use')
      expect(within(card('shop')).getByText('Running')).toBeInTheDocument()

      await act(async () => {
        await new Promise((r) => setTimeout(r, 50))
      })
      expect(screen.getByRole('alert')).toBeInTheDocument()
    })

    it('keeps an error toast past 10 seconds', async () => {
      serveActions(() => ok('running', 'timed_out', 'timed out after 60s'))
      renderPage()
      await findCard('shop')
      vi.useFakeTimers({ shouldAdvanceTime: true })
      fireEvent.click(within(card('shop')).getByRole('switch', { name: 'Stop shop' }))
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
      await findCard('blog')
      fireEvent.click(within(card('blog')).getByRole('switch', { name: 'Start blog' }))
      const alert = await screen.findByRole('alert')
      expect(alert).toHaveTextContent("Couldn't start blog")
      expect(alert).toHaveTextContent('shop is busy with another command')
      // The row goes back to what the server last said.
      expect(within(card('blog')).getByText('Stopped')).toBeInTheDocument()
    })

    it('shows a network failure as an error toast', async () => {
      serveActions(() => {
        throw new TypeError('Failed to fetch')
      })
      renderPage()
      await findCard('blog')
      fireEvent.click(within(card('blog')).getByRole('switch', { name: 'Start blog' }))
      expect(await screen.findByRole('alert')).toHaveTextContent('Failed to fetch')
    })

    it('follows a state event: stopping updates the row and locks the controls', async () => {
      serveActions(() => ok('stopped'))
      renderPage()
      await findCard('shop')

      act(() => {
        FakeEventSource.instances[0].emit('state', {
          dockerUp: true,
          projects: [{ ...projects[0], status: 'stopping', actions: [] }, projects[1]],
        })
      })
      const shop = card('shop')
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
      await findCard('panel')
      const panel = card('panel')
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
      await findCard('shop')
      act(() => {
        FakeEventSource.instances[0].emit('state', {
          dockerUp: false,
          projects: [{ ...projects[0], status: 'unknown', actions: [] }],
        })
      })
      expect(within(card('shop')).getByRole('switch')).toHaveAttribute(
        'title',
        'Docker is not responding',
      )
    })

    it('drops the pending status on the next state event, or after 5 seconds', async () => {
      serveActions(() => ok('stopped'))
      renderPage()
      await findCard('shop')

      // Next state event.
      fireEvent.click(within(card('shop')).getByRole('switch', { name: 'Stop shop' }))
      fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Stop' }))
      expect(await within(card('shop')).findByText('Stopped')).toBeInTheDocument()
      act(() => {
        FakeEventSource.instances[0].emit('state', {
          dockerUp: true,
          projects: [{ ...projects[0], status: 'partial', actions: ['start', 'stop', 'restart'] }],
        })
      })
      expect(within(card('shop')).getByText('Partial')).toBeInTheDocument()

      // After 5 s with no event, the row falls back to the list it has.
      vi.useFakeTimers({ shouldAdvanceTime: true })
      fireEvent.click(within(card('shop')).getByRole('switch', { name: 'Stop shop' }))
      fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Stop' }))
      await waitFor(() => expect(within(card('shop')).getByText('Stopped')).toBeInTheDocument())
      await act(async () => {
        vi.advanceTimersByTime(4900)
      })
      expect(within(card('shop')).getByText('Stopped')).toBeInTheDocument()
      await act(async () => {
        vi.advanceTimersByTime(200)
      })
      expect(within(card('shop')).getByText('Partial')).toBeInTheDocument()
    })

    it('does nothing harmful when the answer arrives after the page is gone', async () => {
      let release: (r: Response) => void = () => {}
      serveActions(() => new Promise<Response>((res) => (release = res)))
      const spy = vi.spyOn(console, 'error').mockImplementation(() => {})
      const { unmount } = renderPage()
      await findCard('blog')
      vi.useFakeTimers({ shouldAdvanceTime: true })
      fireEvent.click(within(card('blog')).getByRole('switch', { name: 'Start blog' }))
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
      await findCard('shop')
      fireEvent.click(within(card('shop')).getByRole('switch', { name: 'Stop shop' }))
      fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Stop' }))
      fireEvent.click(within(card('shop')).getByRole('button', { name: 'More actions for shop' }))
      for (const name of ['Start', 'Restart']) {
        expect(screen.getByRole('menuitem', { name })).toHaveAttribute('aria-disabled', 'true')
      }
      fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' })

      await act(async () => release(ok('stopped', 'ok', '', ['start'])))
      const sw = within(card('shop')).getByRole('switch', { name: 'Start shop' })
      expect(sw).toBeEnabled()
      expect(sw).not.toHaveAttribute('title')
    })
  })
  describe('workspaces, rescan and stop tracking', () => {
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
    const posts = (stub: ReturnType<typeof vi.fn>, url?: string) =>
      stub.mock.calls.filter(
        (c) => (c[1] as RequestInit | undefined)?.method === 'POST' && (!url || c[0] === url),
      )
    const header = () => screen.getByRole('heading', { level: 1 }).parentElement as HTMLElement
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
        await findCard('shop')
        expect(screen.queryByTestId('where')).toBeNull()
        expect(gets(stub, '/api/workspaces')).toHaveLength(1)
      })

      it('stays on the Dashboard when the workspaces fail to load, with no extra alert', async () => {
        route({
          'GET /api/workspaces': () => json(500, { error: 'db locked' }),
          'GET /api/projects': () => json(200, projects),
        })
        renderPage()
        await findCard('shop')
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
        await findCard('shop')
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

    describe('a Not found card', () => {
      it('says since when, and locks the switch', async () => {
        route({
          'GET /api/workspaces': () => json(200, workspaces),
          'GET /api/projects': () => json(200, [projects[0], gone]),
        })
        renderPage()
        const r = await findCard('old-api')
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
        expect(within(card('shop')).queryByText(/Folder missing/)).toBeNull()
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
      const openMenu = (name: string) => {
        fireEvent.click(
          within(card(name)).getByRole('button', { name: `More actions for ${name}` }),
        )
      }

      it('asks first, cancel sends nothing, confirm posts JSON, toasts and hides the row', async () => {
        const stub = setup()
        renderPage()
        await findCard('shop')

        openMenu('shop')
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

        openMenu('shop')
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
        expect(queryCard('shop')).toBeNull()
        expect(card('panel')).toBeInTheDocument()
      })

      it('keeps the row gone when the next live state arrives without it', async () => {
        setup()
        renderPage()
        await findCard('shop')
        openMenu('shop')
        fireEvent.click(screen.getByRole('menuitem', { name: 'Stop tracking' }))
        fireEvent.click(
          within(screen.getByRole('dialog')).getByRole('button', { name: 'Stop tracking' }),
        )
        await waitFor(() => expect(queryCard('shop')).toBeNull())

        // The hub's next state is the truth: without the project it stays gone.
        act(() => {
          FakeEventSource.instances[0].emit('state', { dockerUp: true, projects: [projects[1]] })
        })
        expect(queryCard('shop')).toBeNull()
        expect(card('panel')).toBeInTheDocument()
      })

      it('shows the row again when a live state brings the project back', async () => {
        setup()
        renderPage()
        await findCard('shop')
        openMenu('shop')
        fireEvent.click(screen.getByRole('menuitem', { name: 'Stop tracking' }))
        fireEvent.click(
          within(screen.getByRole('dialog')).getByRole('button', { name: 'Stop tracking' }),
        )
        await waitFor(() => expect(queryCard('shop')).toBeNull())

        // A re-tracked project keeps its id; the overlay must not hide it for good.
        act(() => {
          FakeEventSource.instances[0].emit('state', {
            dockerUp: true,
            projects: [projects[0], projects[1]],
          })
        })
        expect(card('shop')).toBeInTheDocument()
      })

      it('keeps the row hidden when a live state still holds the project with no actions', async () => {
        setup()
        renderPage()
        await findCard('shop')
        openMenu('shop')
        fireEvent.click(screen.getByRole('menuitem', { name: 'Stop tracking' }))
        fireEvent.click(
          within(screen.getByRole('dialog')).getByRole('button', { name: 'Stop tracking' }),
        )
        await waitFor(() => expect(queryCard('shop')).toBeNull())

        // The store write is still out: the hub shows the project held, with no actions.
        act(() => {
          FakeEventSource.instances[0].emit('state', {
            dockerUp: true,
            projects: [{ ...projects[0], actions: [] }, projects[1]],
          })
        })
        expect(queryCard('shop')).toBeNull()

        // Once it is gone from the state, it stays gone; back with actions, it returns.
        act(() => {
          FakeEventSource.instances[0].emit('state', {
            dockerUp: true,
            projects: [projects[0], projects[1]],
          })
        })
        expect(card('shop')).toBeInTheDocument()
      })

      it('sends one request however often it is confirmed while it is out', async () => {
        let release: (r: Response) => void = () => {}
        const stub = setup(() => new Promise<Response>((res) => (release = res)))
        renderPage()
        await findCard('shop')
        for (let i = 0; i < 2; i++) {
          openMenu('shop')
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
        await findCard('old-api')

        openMenu('old-api')
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
        expect(queryCard('old-api')).toBeNull()
      })

      it('shows the server text of a 409 in an error toast and keeps the row', async () => {
        setup(() => json(409, { error: 'shop is busy with another command', code: 'locked' }))
        renderPage()
        await findCard('shop')
        openMenu('shop')
        fireEvent.click(screen.getByRole('menuitem', { name: 'Stop tracking' }))
        fireEvent.click(
          within(screen.getByRole('dialog')).getByRole('button', { name: 'Stop tracking' }),
        )

        const alert = await screen.findByRole('alert')
        expect(alert).toHaveTextContent("Couldn't stop tracking shop")
        expect(alert).toHaveTextContent('shop is busy with another command')
        expect(screen.queryByTestId('toast-success')).toBeNull()
        expect(card('shop')).toBeInTheDocument()
      })

      it('shows no red item for a row whose actions carry neither', async () => {
        setup()
        renderPage()
        await findCard('panel')
        openMenu('panel')
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
      return route({
        'GET /api/projects': () => json(200, [one, none]),
        'POST /api/projects/1/backups': () => json(202, { id: 7 }),
        'GET /api/backups/7': () => json(200, makeBackup()),
      })
    }

    it('starts the backup from the menu and opens its progress', async () => {
      const stub = serveBackup()
      renderPage()
      const shop = await findCard('shop')
      fireEvent.click(within(shop).getByRole('button', { name: 'More actions for shop' }))
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
      const blog = await findCard('blog')
      fireEvent.click(within(blog).getByRole('button', { name: 'More actions for blog' }))
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
