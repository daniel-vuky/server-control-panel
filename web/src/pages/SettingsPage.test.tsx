import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Workspace } from '../api/types.gen'
import { ToastProvider } from '../components/Toast'
import { formatLastScan } from '../lib/time'
import SettingsPage from './SettingsPage'

const scanned = new Date(2026, 0, 5, 9, 42)

const workspaces: Workspace[] = [
  {
    id: 1,
    name: 'projects',
    path: '/home/deploy/projects',
    depth: 1,
    lastScanAt: scanned.toISOString(),
    tracked: 2,
    ignored: 1,
  },
  {
    id: 2,
    name: 'solo',
    path: '/srv/solo',
    depth: 3,
    lastScanAt: scanned.toISOString(),
    tracked: 1,
    ignored: 0,
  },
]

function serve(...answers: Array<[number, unknown]>) {
  let n = 0
  const fetchStub = vi.fn(async () => {
    const [status, body] = answers[Math.min(n++, answers.length - 1)]
    return new Response(JSON.stringify(body), { status })
  })
  vi.stubGlobal('fetch', fetchStub)
  return fetchStub
}

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status })

type Handler = (init?: RequestInit) => Response

/** route stubs fetch by "METHOD /path"; a list of handlers answers one call each, the last repeating. */
function route(table: Record<string, Handler | Handler[]>) {
  const used: Record<string, number> = {}
  const stub = vi.fn(async (url: string, init?: RequestInit) => {
    const key = `${init?.method ?? 'GET'} ${url}`
    const h = table[key]
    if (h === undefined) return json(404, { error: `unrouted ${key}` })
    if (!Array.isArray(h)) return h(init)
    const n = used[key] ?? 0
    used[key] = n + 1
    return h[Math.min(n, h.length - 1)](init)
  })
  vi.stubGlobal('fetch', stub)
  return stub
}

const posts = (stub: ReturnType<typeof vi.fn>) =>
  stub.mock.calls.filter((c) => (c[1] as RequestInit | undefined)?.method === 'POST')

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
  render(
    <MemoryRouter initialEntries={['/settings']}>
      <ToastProvider>
        <Routes>
          <Route path="/settings" element={<SettingsPage />} />
          <Route path="*" element={<Where />} />
        </Routes>
      </ToastProvider>
    </MemoryRouter>,
  )

afterEach(() => vi.unstubAllGlobals())

describe('SettingsPage', () => {
  it('shows the heading, and loading before the answer', () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => new Promise(() => {})),
    )
    renderPage()
    expect(screen.getByRole('heading', { level: 1, name: 'Settings' })).toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent('Loading workspaces')
  })

  it('lists each workspace with its path and summary line', async () => {
    const fetchStub = serve([200, workspaces])
    renderPage()

    const card = screen.getByRole('region', { name: 'Workspaces' })
    expect(await within(card).findByText('/home/deploy/projects')).toBeInTheDocument()
    expect(fetchStub.mock.calls[0]).toEqual(['/api/workspaces', expect.anything()])
    expect(
      within(card).getByText(
        `Depth 1 · 2 projects tracked · 1 ignored · last scanned ${formatLastScan(scanned.toISOString())}`,
      ),
    ).toBeInTheDocument()
    expect(within(card).getByText('/srv/solo')).toBeInTheDocument()
    expect(
      within(card).getByText(
        `Depth 3 · 1 project tracked · 0 ignored · last scanned ${formatLastScan(scanned.toISOString())}`,
      ),
    ).toBeInTheDocument()
    expect(within(card).getByRole('link', { name: 'Add workspace' })).toHaveAttribute(
      'href',
      '/onboarding',
    )
    for (const path of ['/home/deploy/projects', '/srv/solo']) {
      expect(within(card).getByRole('button', { name: `Rescan ${path}` })).toBeInTheDocument()
      expect(within(card).getByRole('button', { name: `Remove ${path}` })).toBeInTheDocument()
    }
  })

  it('shows the empty state, still with Add workspace', async () => {
    serve([200, []])
    renderPage()
    const card = screen.getByRole('region', { name: 'Workspaces' })
    expect(await within(card).findByText('No workspace yet')).toBeInTheDocument()
    expect(within(card).getByRole('link', { name: 'Add workspace' })).toHaveAttribute(
      'href',
      '/onboarding',
    )
  })

  it('shows an error in the card with a retry', async () => {
    const fetchStub = serve([500, { error: 'boom' }], [200, workspaces])
    renderPage()
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Could not load workspaces')
    fireEvent.click(within(alert).getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('/srv/solo')).toBeInTheDocument()
    expect(fetchStub).toHaveBeenCalledTimes(2)
  })

  it('treats a JSON answer that is not a list as an error', async () => {
    serve([200, {}])
    renderPage()
    expect(await screen.findByRole('alert')).toHaveTextContent('Unexpected response')
  })

  it('points the other sections at the issues that will build them', async () => {
    serve([200, []])
    renderPage()
    const issueUrl = (n: number) =>
      `https://github.com/daniel-vuky/server-control-panel/issues/${n}`
    for (const [name, issue] of [
      ['Google Drive', 3],
      ['Alerts', 8],
      ['Backup defaults', 6],
    ] as const) {
      const section = screen.getByRole('region', { name })
      expect(section).toHaveTextContent('Coming in #')
      expect(within(section).getByRole('link', { name: `#${issue}` })).toHaveAttribute(
        'href',
        issueUrl(issue),
      )
    }
    await screen.findByText('No workspace yet')
  })

  describe('Rescan', () => {
    it('posts this workspace only, with JSON, then opens the rescan page', async () => {
      const stub = route({
        'GET /api/workspaces': () => json(200, workspaces),
        'POST /api/rescans': () => json(202, { id: 7 }),
      })
      renderPage()
      const row = (await screen.findByText('/srv/solo')).closest('li') as HTMLElement
      fireEvent.click(within(row).getByRole('button', { name: 'Rescan /srv/solo' }))
      expect(await screen.findByTestId('where')).toHaveTextContent('/rescan/7')
      const [url, init] = posts(stub)[0] as [string, RequestInit]
      expect(url).toBe('/api/rescans')
      expect(init.headers).toMatchObject({ 'Content-Type': 'application/json' })
      expect(JSON.parse(init.body as string)).toEqual({ workspaceId: 2 })
    })

    it('attaches to a running rescan on a 409', async () => {
      route({
        'GET /api/workspaces': () => json(200, workspaces),
        'POST /api/rescans': () =>
          json(409, { error: 'a scan is running', id: 9, path: 'all', depth: 0, rescan: true }),
      })
      renderPage()
      await screen.findByText('/srv/solo')
      fireEvent.click(screen.getByRole('button', { name: 'Rescan /home/deploy/projects' }))
      expect(await screen.findByTestId('where')).toHaveTextContent('/rescan/9')
    })

    it('goes to onboarding with the job id when a first scan runs', async () => {
      route({
        'GET /api/workspaces': () => json(200, workspaces),
        'POST /api/rescans': () =>
          json(409, { error: 'a scan is running', id: 4, path: '/srv/x', depth: 2, rescan: false }),
      })
      renderPage()
      await screen.findByText('/srv/solo')
      fireEvent.click(screen.getByRole('button', { name: 'Rescan /home/deploy/projects' }))
      await screen.findByTestId('where')
      expect(screen.getByTestId('where')).toHaveTextContent('/onboarding')
      expect(JSON.parse(screen.getByTestId('where-state').textContent ?? '')).toEqual({
        scanId: 4,
      })
    })

    it('shows an error toast and stays when the rescan cannot start', async () => {
      route({
        'GET /api/workspaces': () => json(200, workspaces),
        'POST /api/rescans': () => json(500, { error: 'disk full' }),
      })
      renderPage()
      await screen.findByText('/srv/solo')
      fireEvent.click(screen.getByRole('button', { name: 'Rescan /home/deploy/projects' }))
      const alert = await screen.findByRole('alert')
      expect(alert).toHaveTextContent('Could not start the rescan')
      expect(alert).toHaveTextContent('disk full')
      expect(screen.queryByTestId('where')).toBeNull()
    })
  })

  describe('Remove', () => {
    const open = async () => {
      const row = (await screen.findByText('/home/deploy/projects')).closest('li') as HTMLElement
      fireEvent.click(within(row).getByRole('button', { name: 'Remove /home/deploy/projects' }))
      return screen.findByRole('dialog')
    }

    it('asks first, with the spec copy, a danger button and no command box', async () => {
      const stub = route({ 'GET /api/workspaces': () => json(200, workspaces) })
      renderPage()
      const dialog = await open()
      expect(
        within(dialog).getByRole('heading', { name: 'Remove workspace /home/deploy/projects?' }),
      ).toBeInTheDocument()
      expect(within(dialog).getByText('/home/deploy/projects').className).toContain('font-mono')
      expect(dialog).toHaveTextContent(
        'Its 2 tracked projects leave the panel. Nothing on the server is stopped, deleted or changed, and backups on Google Drive stay where they are.',
      )
      expect(within(dialog).getByRole('button', { name: 'Remove' }).className).toContain('bg-stop')
      expect(dialog).not.toHaveTextContent('$ ')
      expect(posts(stub)).toHaveLength(0)
    })

    it('uses the singular for one tracked project', async () => {
      route({ 'GET /api/workspaces': () => json(200, workspaces) })
      renderPage()
      const row = (await screen.findByText('/srv/solo')).closest('li') as HTMLElement
      fireEvent.click(within(row).getByRole('button', { name: 'Remove /srv/solo' }))
      expect(await screen.findByRole('dialog')).toHaveTextContent('Its 1 tracked project leave')
    })

    it('sends nothing on Cancel', async () => {
      const stub = route({ 'GET /api/workspaces': () => json(200, workspaces) })
      renderPage()
      const dialog = await open()
      fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
      expect(screen.queryByRole('dialog')).toBeNull()
      expect(posts(stub)).toHaveLength(0)
    })

    it('posts the removal as JSON, reloads the list and toasts', async () => {
      const stub = route({
        'GET /api/workspaces': [() => json(200, workspaces), () => json(200, [workspaces[1]])],
        'POST /api/workspaces/1/remove': () => json(200, { removed: 2 }),
      })
      renderPage()
      const dialog = await open()
      fireEvent.click(within(dialog).getByRole('button', { name: 'Remove' }))

      const card = screen.getByRole('region', { name: 'Workspaces' })
      await waitFor(() => expect(within(card).queryByText('/home/deploy/projects')).toBeNull())
      expect(screen.queryByRole('dialog')).toBeNull()
      const [url, init] = posts(stub)[0] as [string, RequestInit]
      expect(url).toBe('/api/workspaces/1/remove')
      expect(init.headers).toMatchObject({ 'Content-Type': 'application/json' })
      expect(await within(card).findByText('/srv/solo')).toBeInTheDocument()
      const toast = await screen.findByText(/Removed/)
      expect(toast).toHaveTextContent('Removed /home/deploy/projects')
      expect(posts(stub)).toHaveLength(1)
    })

    it('shows the server text in an error toast on a 409 and keeps the list', async () => {
      route({
        'GET /api/workspaces': () => json(200, workspaces),
        'POST /api/workspaces/1/remove': () =>
          json(409, { error: 'A scan is running; try again when it ends.' }),
      })
      renderPage()
      const dialog = await open()
      fireEvent.click(within(dialog).getByRole('button', { name: 'Remove' }))
      const alert = await screen.findByRole('alert')
      expect(alert).toHaveTextContent('A scan is running; try again when it ends.')
      const card = screen.getByRole('region', { name: 'Workspaces' })
      expect(within(card).getByText('/home/deploy/projects')).toBeInTheDocument()
      expect(screen.queryByRole('dialog')).toBeNull()
    })
  })
})
