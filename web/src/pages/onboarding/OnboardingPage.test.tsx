import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ScanJob, ScanRow } from '../../api/types.gen'
import { AppRoutes } from '../../app/App'

type Listener = (e: MessageEvent) => void

class StubEventSource {
  static instances: StubEventSource[] = []
  url: string
  closed = false
  readyState = 0
  private listeners = new Map<string, Listener[]>()
  constructor(url: string) {
    this.url = url
    StubEventSource.instances.push(this)
  }
  addEventListener(type: string, fn: Listener) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), fn])
  }
  close() {
    this.closed = true
  }
  emit(type: string, data: unknown) {
    act(() => {
      for (const fn of this.listeners.get(type) ?? []) {
        fn(new MessageEvent(type, { data: JSON.stringify(data) }))
      }
    })
  }
}

type Call = { method: string; url: string; body: unknown }
type Route = (c: Call) => [number, unknown] | undefined

let calls: Call[] = []

/** api stubs fetch: each request goes to route; anything unrouted answers 401. */
function api(route: Route) {
  calls = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const c: Call = {
        method: init?.method ?? 'GET',
        url: String(input),
        body: init?.body ? JSON.parse(String(init.body)) : undefined,
      }
      calls.push(c)
      const [status, body] = route(c) ?? [401, { error: 'unauthorized' }]
      return new Response(JSON.stringify(body), { status })
    }),
  )
}

const posts = (url: string) => calls.filter((c) => c.method === 'POST' && c.url === url)

const rows: ScanRow[] = [
  {
    path: '/srv/shop',
    composeFiles: ['/srv/shop/compose.yaml'],
    defaultName: 'shop',
    status: 'running',
    self: false,
    services: 2,
    databases: [
      {
        type: 'postgresql',
        name: 'shop',
        locationKind: 'container',
        location: 'db',
        sources: ['POSTGRES_DB'],
      },
    ],
    warning: '',
  },
  {
    path: '/srv/panel',
    composeFiles: ['/srv/panel/compose.yaml'],
    defaultName: 'panel',
    status: 'running',
    self: true,
    services: 1,
    databases: [],
    warning: '',
  },
  {
    path: '/srv/broken',
    composeFiles: ['/srv/broken/compose.yaml'],
    defaultName: 'broken',
    status: 'unknown',
    self: false,
    services: 0,
    databases: [],
    warning:
      'docker compose config failed: required variable DB_NAME is missing.\nAnother folder uses the compose project name broken; Docker treats them as one project.',
  },
]

const job = (over: Partial<ScanJob> = {}): ScanJob => ({
  id: 7,
  state: 'done',
  path: '/srv',
  depth: 1,
  progress: { examined: 5, total: 5, current: '' },
  error: '',
  result: { rows, foldersScanned: 5, alreadyTracked: 0, ignored: 0, warnings: [] },
  ...over,
})

/** scanRoute answers Scan with 202 {id: 7} and the job read with j. */
const scanRoute =
  (j: ScanJob, extra?: Route): Route =>
  (c) => {
    if (c.method === 'POST' && c.url === '/api/scans') return [202, { id: j.id }]
    if (c.method === 'GET' && c.url === `/api/scans/${j.id}`) return [200, j]
    return extra?.(c)
  }

const renderPage = () =>
  render(
    <MemoryRouter initialEntries={['/onboarding']}>
      <AppRoutes />
    </MemoryRouter>,
  )

const pathInput = () => screen.getByLabelText('Root folder path')

const startScan = (path = '/srv') => {
  fireEvent.change(pathInput(), { target: { value: path } })
  fireEvent.click(screen.getByRole('button', { name: 'Scan' }))
}

const eventSource = async () => {
  await waitFor(() => expect(StubEventSource.instances.length).toBeGreaterThan(0))
  return StubEventSource.instances[StubEventSource.instances.length - 1]
}

/** text finds the element whose own text content is exactly s. */
const text = (s: string) =>
  screen.getByText(
    (_, el) => el?.textContent === s && [...(el?.children ?? [])].every((c) => c.textContent !== s),
  )

/** scanToEnd renders the page, scans /srv and ends the scan with j's state. */
async function scanToEnd(j: ScanJob, extra?: Route) {
  api(scanRoute(j, extra))
  renderPage()
  startScan(j.path)
  const es = await eventSource()
  es.emit('end', { state: j.state })
}

async function scanToResults(j = job(), extra?: Route) {
  await scanToEnd(j, extra)
  await screen.findByRole('heading', { name: /^Found / })
}

const checkbox = (path: string) => screen.getByRole('checkbox', { name: `Track ${path}` })
const nameInput = (path: string) =>
  screen.getByRole('textbox', { name: `Display name for ${path}` })
const addButton = () => screen.getByRole('button', { name: /^Add \d+ project/ })

beforeEach(() => {
  StubEventSource.instances = []
  vi.stubGlobal('EventSource', StubEventSource)
})

afterEach(() => {
  StubEventSource.instances = []
})

describe('onboarding, step 1', () => {
  it('renders the page heading and the two steps', () => {
    renderPage()
    expect(screen.getByRole('heading', { level: 1, name: 'Add a workspace' })).toBeInTheDocument()
    const steps = screen.getByRole('list', { name: 'Setup steps' })
    expect(within(steps).getByText('Workspace')).toBeInTheDocument()
    expect(within(steps).getByText('Scan results')).toBeInTheDocument()
    expect(screen.getByText('Detects: docker compose')).toBeInTheDocument()
  })

  it('shows a 422 message under the field, with aria-invalid', async () => {
    const msg = 'This folder does not exist.'
    api((c) =>
      c.method === 'POST' && c.url === '/api/scans'
        ? [422, { error: msg, field: 'path', code: 'not_found' }]
        : undefined,
    )
    renderPage()
    startScan('/nope')
    expect(await screen.findByText(msg)).toBeInTheDocument()
    expect(pathInput()).toHaveAttribute('aria-invalid', 'true')
    expect(pathInput()).toHaveAccessibleDescription(msg)
    expect(StubEventSource.instances).toHaveLength(0)
  })

  it('defaults the depth control to 1 level and sends the chosen depth', async () => {
    api(scanRoute(job()))
    renderPage()
    expect(screen.getByRole('radio', { name: '1 level' })).toBeChecked()
    expect(screen.getByRole('radio', { name: '2 levels' })).not.toBeChecked()
    expect(screen.getByRole('radio', { name: '3 levels' })).not.toBeChecked()

    fireEvent.click(screen.getByRole('radio', { name: '3 levels' }))
    startScan('/srv')
    await waitFor(() => expect(posts('/api/scans')).toHaveLength(1))
    expect(posts('/api/scans')[0].body).toEqual({ path: '/srv', depth: 3 })
  })

  it('updates the progress text on events and disables the inputs', async () => {
    api(scanRoute(job()))
    renderPage()
    startScan('/srv')
    const es = await eventSource()
    expect(es.url).toBe('/api/scans/7/events')

    es.emit('progress', { examined: 3, total: 12, current: '/srv/shop/compose.yaml' })
    expect(text('Scanning… 3/12 folders')).toBeInTheDocument()
    expect(text('reading /srv/shop/compose.yaml')).toBeInTheDocument()

    es.emit('progress', { examined: 4, total: 12, current: '/srv/web' })
    expect(text('Scanning… 4/12 folders')).toBeInTheDocument()
    expect(text('reading /srv/web')).toBeInTheDocument()

    expect(pathInput()).toBeDisabled()
    expect(screen.getByRole('radio', { name: '2 levels' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Scan' })).toBeDisabled()
  })

  it('attaches to the running scan on a 409', async () => {
    api((c) => {
      if (c.method === 'POST' && c.url === '/api/scans')
        return [409, { error: 'A scan is already running.', id: 4, path: '/srv/busy', depth: 2 }]
      if (c.method === 'GET' && c.url === '/api/scans/4')
        return [200, job({ id: 4, path: '/srv/busy', depth: 2 })]
      return undefined
    })
    renderPage()
    startScan('/srv/other')
    const es = await eventSource()
    expect(es.url).toBe('/api/scans/4/events')
    expect(pathInput()).toHaveValue('/srv/busy')
    expect(screen.getByRole('radio', { name: '2 levels' })).toBeChecked()

    es.emit('progress', { examined: 1, total: 2, current: '/srv/busy/a' })
    expect(text('Scanning… 1/2 folders')).toBeInTheDocument()

    es.emit('end', { state: 'done' })
    expect(await screen.findByRole('heading', { name: 'Found 3 projects' })).toBeInTheDocument()
    expect(text('In /srv/busy · depth 2 · 5 folders scanned')).toBeInTheDocument()
  })

  it.each([
    ['failed', 'docker is not reachable'],
    ['interrupted', 'The panel restarted while this job was running.'],
  ])(
    'an end of %s shows the job error and Scan again posts the same path and depth',
    async (state, error) => {
      await scanToEnd(job({ state, error, result: undefined, path: '/srv', depth: 2 }))
      expect(await screen.findByText(error)).toBeInTheDocument()
      fireEvent.click(screen.getByRole('button', { name: 'Scan again' }))
      await waitFor(() => expect(posts('/api/scans')).toHaveLength(2))
      expect(posts('/api/scans')[1].body).toEqual({ path: '/srv', depth: 2 })
    },
  )
})

describe('onboarding, step 2', () => {
  it('shows the header and starts with every row checked', async () => {
    await scanToResults()
    expect(screen.getByRole('heading', { name: 'Found 3 projects' })).toBeInTheDocument()
    expect(text('In /srv · depth 1 · 5 folders scanned')).toBeInTheDocument()
    for (const r of rows) expect(checkbox(r.path)).toBeChecked()
    expect(nameInput('/srv/shop')).toHaveValue('shop')
    expect(text('Selected 3 / 3')).toBeInTheDocument()
    expect(addButton()).toHaveTextContent('Add 3 projects')
    expect(addButton()).toBeEnabled()
    expect(screen.queryByText(/not shown/)).toBeNull()
  })

  it('shows the database cell and the status word', async () => {
    await scanToResults()
    const shop = checkbox('/srv/shop').closest('tr') as HTMLElement
    expect(within(shop).getByText('PostgreSQL · container db')).toBeInTheDocument()
    expect(within(shop).getByText('Running')).toBeInTheDocument()
    const panel = checkbox('/srv/panel').closest('tr') as HTMLElement
    expect(within(panel).getByText('None detected')).toBeInTheDocument()
    const broken = checkbox('/srv/broken').closest('tr') as HTMLElement
    expect(within(broken).getByText('Unknown')).toBeInTheDocument()
  })

  it.each([
    [2, 1, '2 already tracked · 1 ignored, not shown'],
    [2, 0, '2 already tracked, not shown'],
    [0, 1, '1 ignored, not shown'],
  ])(
    'with %i tracked and %i ignored, says what is not shown',
    async (alreadyTracked, ignored, s) => {
      await scanToResults(
        job({ result: { rows, foldersScanned: 5, alreadyTracked, ignored, warnings: [] } }),
      )
      expect(text(s)).toBeInTheDocument()
    },
  )

  it('says 1 project for a single row', async () => {
    await scanToResults(
      job({
        result: { rows: [rows[0]], foldersScanned: 1, alreadyTracked: 0, ignored: 0, warnings: [] },
      }),
    )
    expect(screen.getByRole('heading', { name: 'Found 1 project' })).toBeInTheDocument()
    expect(addButton()).toHaveTextContent('Add 1 project')
  })

  it('unchecking one gives Selected 2 / 3 and Add 2 projects, and dims the row', async () => {
    await scanToResults()
    fireEvent.click(checkbox('/srv/broken'))
    expect(text('Selected 2 / 3')).toBeInTheDocument()
    expect(addButton()).toHaveTextContent('Add 2 projects')
    expect(checkbox('/srv/broken').closest('tr')).toHaveClass('opacity-55')
    expect(checkbox('/srv/shop').closest('tr')).not.toHaveClass('opacity-55')
  })

  it('sends a rename, and every row, in the track body', async () => {
    await scanToResults(job(), (c) =>
      c.method === 'POST' && c.url === '/api/scans/7/track'
        ? [200, { workspaceId: 1, added: 2 }]
        : undefined,
    )
    fireEvent.change(nameInput('/srv/shop'), { target: { value: '  Shop API ' } })
    fireEvent.click(checkbox('/srv/broken'))
    // A blank name on an unchecked row goes out as its default name.
    fireEvent.change(nameInput('/srv/broken'), { target: { value: '   ' } })
    expect(addButton()).toBeEnabled()
    fireEvent.click(addButton())
    await waitFor(() => expect(posts('/api/scans/7/track')).toHaveLength(1))
    expect(posts('/api/scans/7/track')[0].body).toEqual({
      projects: [
        { path: '/srv/shop', displayName: 'Shop API', track: true },
        { path: '/srv/panel', displayName: 'panel', track: true },
        { path: '/srv/broken', displayName: 'broken', track: false },
      ],
    })
  })

  it('caps a display name at 80 characters', async () => {
    await scanToResults()
    expect(nameInput('/srv/shop')).toHaveAttribute('maxlength', '80')
  })

  it('disables Add when a checked row has a whitespace-only name', async () => {
    await scanToResults()
    fireEvent.change(nameInput('/srv/shop'), { target: { value: '   ' } })
    expect(addButton()).toBeDisabled()
    expect(nameInput('/srv/shop')).toHaveAttribute('aria-invalid', 'true')
    fireEvent.change(nameInput('/srv/shop'), { target: { value: 'shop' } })
    expect(addButton()).toBeEnabled()
  })

  it('disables Add when nothing is selected', async () => {
    await scanToResults()
    for (const r of rows) fireEvent.click(checkbox(r.path))
    expect(text('Selected 0 / 3')).toBeInTheDocument()
    expect(addButton()).toHaveTextContent('Add 0 projects')
    expect(addButton()).toBeDisabled()
  })

  it('Add navigates to the dashboard and shows the toast', async () => {
    await scanToResults(job(), (c) =>
      c.method === 'POST' && c.url === '/api/scans/7/track'
        ? [200, { workspaceId: 1, added: 2 }]
        : undefined,
    )
    fireEvent.click(checkbox('/srv/broken'))
    fireEvent.click(addButton())
    expect(await screen.findByRole('heading', { level: 1, name: 'Dashboard' })).toBeInTheDocument()
    expect(screen.getByText('2 projects added')).toBeInTheDocument()
  })

  it('says 1 project added for one', async () => {
    await scanToResults(job(), (c) =>
      c.method === 'POST' && c.url === '/api/scans/7/track'
        ? [200, { workspaceId: 1, added: 1 }]
        : undefined,
    )
    fireEvent.click(checkbox('/srv/broken'))
    fireEvent.click(checkbox('/srv/panel'))
    fireEvent.click(addButton())
    expect(await screen.findByText('1 project added')).toBeInTheDocument()
  })

  it('shows a track error and stays on the page', async () => {
    const msg = 'Projects can be added only from a scan that finished.'
    await scanToResults(job(), (c) =>
      c.method === 'POST' && c.url === '/api/scans/7/track' ? [409, { error: msg }] : undefined,
    )
    fireEvent.click(addButton())
    expect(await screen.findByText(msg)).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Found 3 projects' })).toBeInTheDocument()
    expect(addButton()).toBeEnabled()
  })

  it('shows the Monitor only tag on the self row only', async () => {
    await scanToResults()
    const panel = checkbox('/srv/panel').closest('tr') as HTMLElement
    expect(within(panel).getByText('Monitor only')).toBeInTheDocument()
    expect(screen.getAllByText('Monitor only')).toHaveLength(1)
  })

  it('shows a row warning on several lines', async () => {
    await scanToResults()
    const broken = checkbox('/srv/broken').closest('tr') as HTMLElement
    const w = within(broken).getByText(/required variable DB_NAME is missing/)
    expect(w.textContent).toBe(rows[2].warning)
    expect(w).toHaveClass('whitespace-pre-line')
  })

  it('Rescan posts the same path and depth', async () => {
    await scanToResults(job({ depth: 2 }))
    fireEvent.click(screen.getByRole('button', { name: 'Rescan' }))
    await waitFor(() => expect(posts('/api/scans')).toHaveLength(2))
    expect(posts('/api/scans')[1].body).toEqual({ path: '/srv', depth: 2 })
    expect(await screen.findByText(/^Scanning…/)).toBeInTheDocument()
  })
})

describe('onboarding, nothing found', () => {
  const empty = (depth: number) =>
    job({
      depth,
      result: { rows: [], foldersScanned: 4, alreadyTracked: 0, ignored: 0, warnings: [] },
    })

  it('at depth 1 offers Scan again at depth 2, which posts depth 2', async () => {
    await scanToEnd(empty(1))
    expect(
      await screen.findByText(
        (_, el) =>
          el?.textContent === 'No compose project found in /srv at depth 1.' && el.tagName === 'H2',
      ),
    ).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Scan again at depth 2' }))
    await waitFor(() => expect(posts('/api/scans')).toHaveLength(2))
    expect(posts('/api/scans')[1].body).toEqual({ path: '/srv', depth: 2 })
  })

  it('at depth 3 offers no depth+1 button', async () => {
    await scanToEnd(empty(3))
    expect(await screen.findByRole('button', { name: 'Check the path' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^Scan again at depth/ })).toBeNull()
  })

  it('Check the path returns to step 1 and focuses the path field', async () => {
    await scanToEnd(empty(1))
    fireEvent.click(await screen.findByRole('button', { name: 'Check the path' }))
    expect(pathInput()).toHaveFocus()
    expect(pathInput()).toHaveValue('/srv')
    expect(pathInput()).toBeEnabled()
  })
})
