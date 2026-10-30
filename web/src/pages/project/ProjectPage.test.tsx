import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { useLocation } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { projectRoute } from '../../app/App'
import { formatLastScan } from '../../lib/time'
import { FakeEventSource } from '../../test/fakeEventSource'
import { makeContainer, makeProject, pushState, renderProject } from '../../test/projects'
import { renderApp } from '../../test/renderApp'
import { useProjectOutlet } from './projectContext'

const HOUR = 3600_000

const shop = makeProject({
  status: 'partial',
  publicDomain: 'api.shop.test',
  containers: [
    makeContainer('app'),
    makeContainer('worker', { state: 'exited', status: 'Exited', exitReason: 'Exited (1)' }),
    makeContainer('cache', { name: 'cache', state: '', status: 'Not created', image: '' }),
  ],
})

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status })

/** serve answers each POST with next(); everything else is the setup's 401. */
function serve(next: () => Promise<Response> | Response) {
  const stub = vi.fn(async (_url: string, init?: RequestInit) => {
    if (init?.method === 'POST') return next()
    return json(401, { error: 'unauthorized' })
  })
  vi.stubGlobal('fetch', stub)
  return stub
}
const posts = (stub: ReturnType<typeof vi.fn>) =>
  stub.mock.calls.filter((c) => (c[1] as RequestInit | undefined)?.method === 'POST')

const header = () => screen.getByTestId('project-header')

/** Leave stands in for the Settings tab: it leaves through the page's action flow. */
function Leave() {
  const { raw, actions } = useProjectOutlet()
  return (
    <button type="button" onClick={() => actions.leave(raw)}>
      Leave
    </button>
  )
}

/** Where shows the path the page went to. */
function Where() {
  return <div data-testid="where">{useLocation().pathname}</div>
}

beforeEach(() => {
  FakeEventSource.instances = []
  vi.stubGlobal('EventSource', FakeEventSource)
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('project page', () => {
  it('shows a skeleton before the first state', () => {
    renderProject('/projects/1')
    expect(screen.getByRole('status')).toHaveTextContent('Loading project…')
    expect(screen.queryByRole('heading', { level: 1 })).toBeNull()
  })

  it('renders breadcrumb back to the dashboard', () => {
    renderProject('/projects/1')
    pushState([shop])
    const crumbs = screen.getByRole('navigation', { name: 'Breadcrumb' })
    const back = within(crumbs).getByRole('link', { name: 'Projects' })
    expect(back).toHaveAttribute('href', '/')
    expect(within(crumbs).getByText('shop')).toHaveAttribute('aria-current', 'page')
    fireEvent.click(back)
    expect(screen.getByTestId('where').textContent).toBe('/')
  })

  it('header shows name, status, containers count, path, domain link and uptime', () => {
    const since = new Date(Date.now() - (2 * 24 + 3) * HOUR - 5 * 60_000).toISOString()
    renderProject('/projects/1')
    pushState([{ ...shop, upSince: since }])
    const h = header()
    expect(within(h).getByRole('heading', { level: 1 })).toHaveTextContent('shop')
    expect(within(h).getByText('Partial')).toBeInTheDocument()
    // The Not created service is not a container.
    expect(h).toHaveTextContent('Docker · 2 containers')
    expect(within(h).getByText('/srv/work/shop')).toBeInTheDocument()
    const domain = within(h).getByRole('link', { name: 'api.shop.test' })
    expect(domain).toHaveAttribute('href', 'https://api.shop.test')
    expect(domain).toHaveAttribute('target', '_blank')
    expect(h).toHaveTextContent('Uptime 2d 3h')
    expect(within(h).queryByText('Monitor only')).toBeNull()
  })

  it('hides an unset domain, shows — for no uptime and tags the panel project', () => {
    renderProject('/projects/1')
    pushState([makeProject({ self: true, actions: ['untrack'] })])
    const h = header()
    expect(within(h).queryByRole('link')).toBeNull()
    expect(h).toHaveTextContent('Uptime —')
    expect(h).toHaveTextContent('Docker · 0 containers')
    expect(within(h).getByText('Monitor only')).toBeInTheDocument()
  })

  it('lifecycle buttons follow actions', () => {
    serve(() => json(200, {}))
    renderProject('/projects/1')
    pushState([shop])
    const h = header()
    const start = within(h).getByRole('button', { name: 'Start' })
    expect(start).toBeDisabled()
    expect(start).toHaveAttribute('title', 'Not allowed while Partial')
    expect(within(h).getByRole('button', { name: 'Stop' })).toBeEnabled()
    expect(within(h).getByRole('button', { name: 'Restart' })).toBeEnabled()

    fireEvent.click(within(h).getByRole('button', { name: 'Stop' }))
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByRole('heading')).toHaveTextContent('Stop shop?')
    expect(dialog).toHaveTextContent('$ docker compose stop · timeout 60s')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))

    // A Partial project is up, so Restart asks first too.
    fireEvent.click(within(h).getByRole('button', { name: 'Restart' }))
    expect(within(screen.getByRole('dialog')).getByRole('heading')).toHaveTextContent(
      'Restart shop?',
    )
  })

  it('starts at once, shows the in-between status, then the toast', async () => {
    let release: (r: Response) => void = () => {}
    const stub = serve(() => new Promise<Response>((res) => (release = res)))
    renderProject('/projects/1')
    pushState([makeProject({ status: 'stopped', actions: ['start', 'untrack'] })])
    fireEvent.click(within(header()).getByRole('button', { name: 'Start' }))
    expect(screen.queryByRole('dialog')).toBeNull()
    await waitFor(() => expect(posts(stub)).toHaveLength(1))
    expect((posts(stub)[0] as [string])[0]).toBe('/api/projects/1/start')
    expect(within(header()).getByText('Starting…')).toBeInTheDocument()
    expect(within(header()).getByRole('button', { name: 'Start' })).toBeDisabled()

    await act(async () =>
      release(json(200, { outcome: 'ok', status: 'running', actions: ['stop'], message: '' })),
    )
    expect(await screen.findByTestId('toast-success')).toHaveTextContent('Started shop')
    expect(within(header()).getByText('Running')).toBeInTheDocument()
  })

  it('back up now is disabled with tooltip', () => {
    renderProject('/projects/1')
    pushState([shop])
    const backup = within(header()).getByRole('button', { name: 'Back up now' })
    expect(backup).toBeDisabled()
    expect(backup).toHaveAttribute('title', 'Backups are not set up yet')
  })

  it('tabs are links with their own URLs and Overview first', () => {
    renderProject('/projects/1')
    pushState([shop])
    const tabs = within(screen.getByRole('tablist')).getAllByRole('tab')
    expect(tabs.map((t) => t.textContent)).toEqual([
      'Overview',
      'Logs',
      'Database & Backup',
      'Settings',
    ])
    expect(tabs.map((t) => t.getAttribute('href'))).toEqual([
      '/projects/1',
      '/projects/1/logs',
      '/projects/1/database',
      '/projects/1/settings',
    ])
    expect(tabs[0]).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByRole('tabpanel')).toHaveAccessibleName('Overview')

    fireEvent.click(tabs[2])
    const after = within(screen.getByRole('tablist')).getAllByRole('tab')
    expect(after[0]).toHaveAttribute('aria-selected', 'false')
    expect(after[2]).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByRole('tabpanel')).toHaveAccessibleName('Database & Backup')
  })

  it('opens the tab its URL names', () => {
    renderProject('/projects/1/settings')
    pushState([shop])
    expect(screen.getByRole('tab', { name: 'Settings' })).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByRole('tab', { name: 'Overview' })).toHaveAttribute('aria-selected', 'false')
  })

  it('arrow keys move between tabs', () => {
    renderProject('/projects/1')
    pushState([shop])
    const tabs = within(screen.getByRole('tablist')).getAllByRole('tab')
    // Only the open tab is in the Tab order.
    expect(tabs.map((t) => t.getAttribute('tabindex'))).toEqual(['0', '-1', '-1', '-1'])
    tabs[0].focus()
    fireEvent.keyDown(tabs[0], { key: 'ArrowRight' })
    expect(tabs[1]).toHaveFocus()
    fireEvent.keyDown(tabs[1], { key: 'ArrowLeft' })
    expect(tabs[0]).toHaveFocus()
    fireEvent.keyDown(tabs[0], { key: 'ArrowLeft' })
    expect(tabs[3]).toHaveFocus()
    fireEvent.keyDown(tabs[3], { key: 'ArrowRight' })
    expect(tabs[0]).toHaveFocus()
    fireEvent.keyDown(tabs[0], { key: 'End' })
    expect(tabs[3]).toHaveFocus()
    fireEvent.keyDown(tabs[3], { key: 'Home' })
    expect(tabs[0]).toHaveFocus()
  })

  it('uptime ticks once a minute', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-10-30T12:00:00Z'))
    renderProject('/projects/1')
    pushState([{ ...shop, upSince: '2026-10-30T11:15:00Z' }])
    expect(header()).toHaveTextContent('Uptime 45m')
    act(() => vi.advanceTimersByTime(59_000))
    expect(header()).toHaveTextContent('Uptime 45m')
    act(() => vi.advanceTimersByTime(61_000))
    expect(header()).toHaveTextContent('Uptime 47m')
  })

  it('database tab shows a count badge', () => {
    const db = {
      type: 'postgresql',
      name: 'shop',
      locationKind: 'container',
      location: 'db',
      sources: [],
    }
    renderProject('/projects/1')
    pushState([{ ...shop, databases: [db, { ...db, name: 'audit' }] }])
    const tab = screen.getByRole('tab', { name: /Database & Backup/ })
    expect(within(tab).getByText('2')).toBeInTheDocument()
    expect(tab).toHaveAccessibleName('Database & Backup, 2 databases')

    pushState([{ ...shop, databases: [] }])
    expect(screen.getByRole('tab', { name: 'Database & Backup' })).toBeInTheDocument()
  })

  it('untracked id shows not tracked', () => {
    renderProject('/projects/9')
    pushState([shop])
    expect(screen.getByText('This project is not tracked.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Back to the Dashboard' })).toHaveAttribute('href', '/')
    expect(screen.queryByRole('tablist')).toBeNull()
  })

  it('a bad id is not tracked, even before the first state', () => {
    renderProject('/projects/shop-api')
    expect(screen.getByText('This project is not tracked.')).toBeInTheDocument()
  })

  it('switches to not tracked when the project leaves the live list', () => {
    renderProject('/projects/1')
    pushState([shop])
    expect(screen.getByRole('tablist')).toBeInTheDocument()
    pushState([])
    expect(screen.getByText('This project is not tracked.')).toBeInTheDocument()
  })

  it('keeps the header and tabs mounted across a live update', () => {
    renderProject('/projects/1')
    pushState([shop])
    const tab = screen.getByRole('tab', { name: 'Logs' })
    pushState([{ ...shop, cpuPercent: 12 }])
    expect(screen.getByRole('tab', { name: 'Logs' })).toBe(tab)
  })

  it('not found project shows the pill, the missing line and disabled lifecycle buttons', () => {
    const missing = makeProject({
      status: 'notfound',
      actions: ['remove'],
      missingSince: '2026-10-30T09:42:00Z',
    })
    renderProject('/projects/1')
    pushState([missing])
    const h = header()
    expect(within(h).getByText('Not found')).toBeInTheDocument()
    expect(h).toHaveTextContent(
      `Folder missing since the ${formatLastScan('2026-10-30T09:42:00Z')} scan. Not removed automatically.`,
    )
    for (const name of ['Start', 'Stop', 'Restart']) {
      expect(within(h).getByRole('button', { name })).toBeDisabled()
    }
    // Remove from list lives in the Settings tab (spec §8), not the header.
    expect(within(h).queryByRole('button', { name: 'Remove from list' })).toBeNull()
  })

  it("a tab's leave asks in the page's dialog, then returns to the dashboard", async () => {
    const stub = serve(() => json(200, { removed: 1 }))
    renderApp('/projects/1/settings', {
      routes: [
        { ...projectRoute, children: [{ path: 'settings', element: <Leave /> }] },
        { path: '*', element: <Where /> },
      ],
    })
    pushState([makeProject({ status: 'notfound', actions: ['remove'] })])

    fireEvent.click(screen.getByRole('button', { name: 'Leave' }))
    const dialog = screen.getByRole('dialog')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Remove from list' }))
    await waitFor(() => expect(posts(stub)).toHaveLength(1))
    expect((posts(stub)[0] as [string])[0]).toBe('/api/projects/1/untrack')
    expect(await screen.findByTestId('toast-success')).toHaveTextContent('Removed shop')
    // The router navigates after the removal settles.
    expect((await screen.findByTestId('where')).textContent).toBe('/')
  })

  it('offers no Remove from list for a present project', () => {
    renderProject('/projects/1')
    pushState([shop])
    expect(within(header()).queryByRole('button', { name: 'Remove from list' })).toBeNull()
  })
})
