import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ScanJob, ScanResult, ScanRow } from '../api/types.gen'
import { renderApp } from '../test/renderApp'

type Listener = (e: MessageEvent) => void

class StubEventSource {
  static instances: StubEventSource[] = []
  url: string
  closed = false
  readyState = 0
  private listeners = new Map<string, Listener[]>()
  constructor(url: string) {
    this.url = url
    // The app shell's live-state stream is not the scan stream under test.
    if (url !== '/api/events') StubEventSource.instances.push(this)
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

const row = (path: string, over: Partial<ScanRow> = {}): ScanRow => ({
  path,
  composeFiles: [`${path}/compose.yaml`],
  defaultName: path.split('/').pop() ?? path,
  status: 'stopped',
  self: false,
  services: 1,
  databases: [],
  warning: '',
  workspaceId: 1,
  new: true,
  ignored: false,
  ...over,
})

const NEW_A = '/srv/alpha'
const NEW_B = '/srv/beta'
const OLD = '/srv/old'

const rows: ScanRow[] = [
  row(NEW_A),
  row(NEW_B),
  row(OLD, { defaultName: 'old', new: false, ignored: true }),
]

const result = (over: Partial<ScanResult> = {}): ScanResult => ({
  rows,
  foldersScanned: 9,
  alreadyTracked: 0,
  ignored: 0,
  warnings: [],
  new: 2,
  ignoredFound: 1,
  refreshed: 3,
  notFound: 4,
  notFoundPaths: [],
  workspaces: ['/srv', '/opt'],
  ...over,
})

const job = (over: Partial<ScanJob> = {}): ScanJob => ({
  id: 7,
  state: 'done',
  path: 'all workspaces',
  depth: 0,
  rescan: true,
  workspaceIds: [1, 2],
  progress: { examined: 9, total: 9, current: '', workspace: 2, workspaces: 2 },
  error: '',
  result: result(),
  ...over,
})

/** jobRoute answers the job reads for scan 7 with j. */
const jobRoute =
  (j: ScanJob, extra?: Route): Route =>
  (c) => {
    if (c.method === 'GET' && c.url === `/api/scans/${j.id}`) return [200, j]
    return extra?.(c)
  }

const renderAt = (entries = ['/', '/rescan/7'], index = entries.length - 1) =>
  renderApp(entries, { index })

const eventSource = async () => {
  await waitFor(() => expect(StubEventSource.instances.length).toBeGreaterThan(0))
  return StubEventSource.instances[StubEventSource.instances.length - 1]
}

/** text finds the element whose own text content is exactly s. */
const text = (s: string) =>
  screen.getByText(
    (_, el) => el?.textContent === s && [...(el?.children ?? [])].every((c) => c.textContent !== s),
  )

/** toEnd renders the page and ends the scan with j's state. */
async function toEnd(j: ScanJob, extra?: Route, entries?: string[]) {
  api(jobRoute(j, extra))
  renderAt(entries)
  const es = await eventSource()
  es.emit('end', { state: j.state })
}

async function toResults(j = job(), extra?: Route) {
  await toEnd(j, extra)
  await screen.findByRole('heading', { name: 'Rescan results' })
}

const checkbox = (path: string) => screen.getByRole('checkbox', { name: `Track ${path}` })
const maybeCheckbox = (path: string) => screen.queryByRole('checkbox', { name: `Track ${path}` })
const nameInput = (path: string) =>
  screen.getByRole('textbox', { name: `Display name for ${path}` })
const addButton = () => screen.getByRole('button', { name: /^Add \d+ projects?$/ })

beforeEach(() => {
  StubEventSource.instances = []
  vi.stubGlobal('EventSource', StubEventSource)
})

afterEach(() => {
  StubEventSource.instances = []
})

describe('rescan page, progress', () => {
  it('watches the job and prefixes the progress with the workspace', async () => {
    api(jobRoute(job({ state: 'running', result: undefined })))
    renderAt()
    const es = await eventSource()
    expect(es.url).toBe('/api/scans/7/events')
    es.emit('progress', { examined: 3, total: 12, current: '/srv/a', workspace: 2, workspaces: 3 })
    expect(text('Workspace 2/3 · Scanning… 3/12 folders')).toBeInTheDocument()
    expect(text('reading /srv/a')).toBeInTheDocument()
  })

  it('shows no prefix without workspaces', async () => {
    api(jobRoute(job({ state: 'running', result: undefined })))
    renderAt()
    const es = await eventSource()
    es.emit('progress', { examined: 1, total: 2, current: '', workspace: 0, workspaces: 0 })
    expect(text('Scanning… 1/2 folders')).toBeInTheDocument()
  })

  it('redirects a first-scan job to onboarding, which takes over the scan from the state', async () => {
    api(jobRoute(job({ rescan: false, workspaceIds: [], path: '/srv', depth: 2 })))
    renderAt()
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Add project folder' }),
    ).toBeVisible()
    // Onboarding shows the running scan: its own watch, the job's path and
    // depth, locked inputs, and no new scan posted.
    await waitFor(() =>
      expect(
        StubEventSource.instances.filter((s) => s.url === '/api/scans/7/events' && !s.closed),
      ).toHaveLength(1),
    )
    const input = screen.getByLabelText('Root folder path')
    await waitFor(() => expect(input).toHaveValue('/srv'))
    expect(input).toBeDisabled()
    expect(screen.getByRole('radio', { name: '2 levels' })).toBeChecked()
    expect(posts('/api/scans')).toHaveLength(0)
  })

  it('stops its own watch after the first-scan redirect', async () => {
    api(jobRoute(job({ rescan: false, workspaceIds: [], path: '/srv', depth: 1 })))
    renderAt()
    const es = await eventSource()
    await screen.findByRole('heading', { level: 1, name: 'Add project folder' })
    expect(es.closed).toBe(true)
  })

  it('shows a not-found message without Retry for an id that is not a job number', async () => {
    api(jobRoute(job()))
    renderAt(['/', '/rescan/abc'])
    expect(await screen.findByText('This rescan was not found.')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull()
    expect(StubEventSource.instances).toHaveLength(0)
    expect(calls.filter((c) => c.url.startsWith('/api/scans'))).toHaveLength(0)
    fireEvent.click(screen.getByRole('button', { name: 'Back to Dashboard' }))
    expect(await screen.findByRole('heading', { level: 1, name: 'Dashboard' })).toBeVisible()
  })
})

describe('rescan page, unknown job', () => {
  it('shows the not-found box, without Retry or a new rescan, when the job does not exist', async () => {
    api((c) =>
      c.method === 'GET' && c.url === '/api/scans/999' ? [404, { error: 'not found' }] : undefined,
    )
    renderAt(['/', '/rescan/999'])
    expect(await screen.findByText('This rescan was not found.')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull()
    expect(screen.getByRole('button', { name: 'Back to Dashboard' })).toBeInTheDocument()
    expect(posts('/api/rescans')).toHaveLength(0)
  })
})

describe('rescan page, results', () => {
  it('shows the summary line', async () => {
    await toResults()
    expect(text('Rescanned 2 workspaces · 2 new · 4 not found · 1 ignored')).toBeInTheDocument()
  })

  it('shows the New rows checked and tagged, with editable names', async () => {
    await toResults()
    expect(checkbox(NEW_A)).toBeChecked()
    expect(checkbox(NEW_B)).toBeChecked()
    expect(screen.getAllByText('New')).toHaveLength(2)
    fireEvent.change(nameInput(NEW_A), { target: { value: 'renamed' } })
    expect(nameInput(NEW_A)).toHaveValue('renamed')
    expect(screen.getByRole('button', { name: 'Add 2 projects' })).toBeEnabled()
  })

  it('has no Rescan button and keeps the page out of the nav', async () => {
    await toResults()
    expect(screen.queryByRole('button', { name: 'Rescan' })).toBeNull()
    const nav = screen.getByRole('navigation', { name: 'Main navigation' })
    expect(nav).not.toHaveTextContent(/rescan/i)
  })

  it('hides the ignored rows until Show ignored (N) is checked, unchecked', async () => {
    await toResults()
    expect(maybeCheckbox(OLD)).toBeNull()
    const toggle = screen.getByRole('checkbox', { name: 'Show ignored (1)' })
    fireEvent.click(toggle)
    expect(checkbox(OLD)).not.toBeChecked()
    expect(screen.getAllByText('New')).toHaveLength(2)
    expect(addButton()).toHaveTextContent('Add 2 projects')
    fireEvent.click(toggle)
    expect(maybeCheckbox(OLD)).toBeNull()
  })

  it('Add posts only the checked rows with track true, then goes to the dashboard with a toast', async () => {
    await toResults(job(), (c) =>
      c.method === 'POST' && c.url === '/api/scans/7/track'
        ? [200, { workspaceId: 0, added: 2 }]
        : undefined,
    )
    fireEvent.click(checkbox(NEW_B))
    fireEvent.click(screen.getByRole('checkbox', { name: 'Show ignored (1)' }))
    fireEvent.click(checkbox(OLD))
    fireEvent.change(nameInput(NEW_A), { target: { value: '  renamed ' } })
    fireEvent.click(addButton())
    expect(await screen.findByRole('heading', { level: 1, name: 'Dashboard' })).toBeVisible()
    expect(screen.getByText('2 projects added')).toBeInTheDocument()
    expect(posts('/api/scans/7/track')[0].body).toEqual({
      projects: [
        { path: NEW_A, displayName: 'renamed', track: true },
        { path: OLD, displayName: 'old', track: true },
      ],
    })
  })

  it('does not post a hidden ignored row', async () => {
    await toResults(job(), (c) =>
      c.method === 'POST' && c.url === '/api/scans/7/track'
        ? [200, { workspaceId: 0, added: 1 }]
        : undefined,
    )
    const toggle = screen.getByRole('checkbox', { name: 'Show ignored (1)' })
    fireEvent.click(toggle)
    fireEvent.click(checkbox(OLD))
    fireEvent.click(toggle)
    fireEvent.click(checkbox(NEW_B))
    fireEvent.click(addButton())
    await waitFor(() => expect(posts('/api/scans/7/track')).toHaveLength(1))
    expect(posts('/api/scans/7/track')[0].body).toEqual({
      projects: [{ path: NEW_A, displayName: 'alpha', track: true }],
    })
  })

  it('disables Add for a blank name or nothing selected', async () => {
    await toResults()
    fireEvent.change(nameInput(NEW_A), { target: { value: '   ' } })
    expect(addButton()).toBeDisabled()
    fireEvent.click(checkbox(NEW_A))
    fireEvent.click(checkbox(NEW_B))
    expect(screen.getByRole('button', { name: 'Add 0 projects' })).toBeDisabled()
  })

  it('shows a track error and stays on the page', async () => {
    const msg = 'These projects were already added.'
    await toResults(job(), (c) =>
      c.method === 'POST' && c.url === '/api/scans/7/track' ? [409, { error: msg }] : undefined,
    )
    fireEvent.click(addButton())
    expect(await screen.findByText(msg)).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Rescan results' })).toBeInTheDocument()
    expect(addButton()).toBeEnabled()
  })

  it('stays with warnings and offers Back to Dashboard when there is nothing to add', async () => {
    const warn = 'Skipped /opt: the folder is empty.'
    await toResults(
      job({ result: result({ rows: [], new: 0, ignoredFound: 0, warnings: [warn] }) }),
    )
    expect(screen.getByText(warn)).toBeInTheDocument()
    expect(text('Rescanned 2 workspaces · 0 new · 4 not found · 0 ignored')).toBeInTheDocument()
    expect(screen.queryByText(/Rescan finished/)).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Back to Dashboard' }))
    expect(await screen.findByRole('heading', { level: 1, name: 'Dashboard' })).toBeVisible()
  })
})

describe('rescan page, nothing to offer', () => {
  const empty = () =>
    job({ result: result({ rows: [], new: 0, ignoredFound: 0, notFound: 2, warnings: [] }) })

  it('shows a toast and goes back one entry', async () => {
    await toEnd(empty(), undefined, ['/settings', '/rescan/7'])
    expect(await screen.findByRole('heading', { level: 1, name: 'Settings' })).toBeVisible()
    expect(screen.getByText('Rescan finished: no new projects, 2 not found')).toBeInTheDocument()
  })

  it('goes to the dashboard when there is no history', async () => {
    await toEnd(empty(), undefined, ['/rescan/7'])
    expect(await screen.findByRole('heading', { level: 1, name: 'Dashboard' })).toBeVisible()
    expect(screen.getByText('Rescan finished: no new projects, 2 not found')).toBeInTheDocument()
  })
})

describe('rescan page, failure', () => {
  const failed = (over: Partial<ScanJob> = {}) =>
    job({ state: 'failed', error: 'docker is not reachable', result: undefined, ...over })

  it('shows the error and Retry rescans the one workspace', async () => {
    await toEnd(failed({ workspaceIds: [5] }), (c) =>
      c.method === 'POST' && c.url === '/api/rescans' ? [202, { id: 8 }] : undefined,
    )
    expect(await screen.findByText('docker is not reachable')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    await waitFor(() => expect(posts('/api/rescans')).toHaveLength(1))
    expect(posts('/api/rescans')[0].body).toEqual({ workspaceId: 5 })
    await waitFor(() =>
      expect(StubEventSource.instances.map((s) => s.url)).toContain('/api/scans/8/events'),
    )
  })

  it('shows the banner and Retry with no card around them', async () => {
    await toEnd(failed({ workspaceIds: [5] }))
    const banner = (await screen.findByText('docker is not reachable')).closest('[role="alert"]')
    expect(banner).toHaveClass('bg-error-bg')
    const title = screen.getByRole('heading', { level: 1, name: 'Rescan' })
    expect(title.parentElement).not.toHaveClass('border')
    expect(title.parentElement).not.toHaveClass('bg-surface')
    // The same size as the results step's h1.
    expect(title).toHaveClass('text-xl')
  })

  it('Retry rescans everything when the job covered several workspaces', async () => {
    await toEnd(failed({ workspaceIds: [1, 2] }), (c) =>
      c.method === 'POST' && c.url === '/api/rescans' ? [202, { id: 8 }] : undefined,
    )
    fireEvent.click(await screen.findByRole('button', { name: 'Retry' }))
    await waitFor(() => expect(posts('/api/rescans')).toHaveLength(1))
    expect(posts('/api/rescans')[0].body).toEqual({ workspaceId: 0 })
  })

  it('Retry attaches to the rescan that already runs on a 409', async () => {
    await toEnd(failed({ workspaceIds: [1] }), (c) =>
      c.method === 'POST' && c.url === '/api/rescans'
        ? [
            409,
            {
              error: 'A scan is already running.',
              id: 9,
              path: 'all workspaces',
              depth: 0,
              rescan: true,
            },
          ]
        : undefined,
    )
    fireEvent.click(await screen.findByRole('button', { name: 'Retry' }))
    await waitFor(() =>
      expect(StubEventSource.instances.map((s) => s.url)).toContain('/api/scans/9/events'),
    )
  })
})
