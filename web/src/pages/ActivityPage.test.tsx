import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { useLocation } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ActivityEntry, ActivityPage as Page } from '../api/types.gen'
import { makeEntry } from '../test/activity'
import { json, SETTINGS } from '../test/backupsPage'
import { FakeEventSource } from '../test/fakeEventSource'
import { makeProject, pushState } from '../test/projects'
import { renderApp } from '../test/renderApp'
import ActivityPage from './ActivityPage'

/** 10:00 on Sat 1 Nov 2025 in the server zone (+07:00). */
const NOW = '2025-11-01T03:00:00Z'

function Where() {
  const loc = useLocation()
  return <div data-testid="where">{`${loc.pathname}${loc.search}`}</div>
}

type Serve = (url: string) => Response | undefined

/** serveActivity answers the page's reads: the zone, and /api/activity by `list` (one page of `rows` by default). */
function serveActivity(rows: ActivityEntry[] = [], list?: Serve) {
  const stub = vi.fn(async (url: string) => {
    if (url === '/api/backup-settings') return json(200, SETTINGS)
    if (url === '/api/activity' || url.startsWith('/api/activity?')) {
      return list?.(url) ?? json(200, { entries: rows, more: false } satisfies Page)
    }
    return json(404, { error: 'not found' })
  })
  vi.stubGlobal('fetch', stub)
  return stub
}

const activityCalls = (stub: ReturnType<typeof vi.fn>) =>
  stub.mock.calls.map((c) => String(c[0])).filter((u) => u.startsWith('/api/activity'))

function open(path = '/activity', projects = [makeProject()]) {
  const r = renderApp(path, {
    routes: [
      { path: '/activity', element: <ActivityPage /> },
      { path: '*', element: <Where /> },
    ],
  })
  if (projects.length > 0) pushState(projects)
  return r
}

const rows = (...entries: ActivityEntry[]) => entries

const E = (id: number, more: Partial<ActivityEntry> = {}) => makeEntry({ id, ...more })

beforeEach(() => {
  FakeEventSource.instances = []
  vi.stubGlobal('EventSource', FakeEventSource)
  vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval'] })
  vi.setSystemTime(new Date(NOW))
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('ActivityPage', () => {
  it('has the header copy', async () => {
    serveActivity([E(1)])
    open()
    expect(screen.getByRole('heading', { level: 1, name: 'Activity' })).toBeInTheDocument()
    expect(
      screen.getByText('Who did what, on which project, and the outcome · plus system alerts'),
    ).toBeInTheDocument()
    await screen.findByRole('list', { name: /Today/ })
  })

  it('shows a skeleton while loading', () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => new Promise(() => {})),
    )
    open()
    expect(screen.getByRole('status', { name: 'Loading' })).toBeInTheDocument()
  })

  it('groups the days newest first with each row in its words', async () => {
    serveActivity(
      rows(
        E(5, { at: '2025-11-01T10:12:00+07:00', finishedAt: '2025-11-01T10:12:12+07:00' }),
        E(4, {
          at: '2025-11-01T02:00:00+07:00',
          finishedAt: '2025-11-01T02:00:41+07:00',
          actor: 'schedule',
          actorKind: 'schedule',
          action: 'backup.run',
          kind: 'backup',
          target: 'shop · shop_production',
          reason: '84.2 MB · 41s',
        }),
        E(3, {
          at: '2025-10-31T02:01:00+07:00',
          finishedAt: '2025-10-31T02:01:10+07:00',
          action: 'scan.start',
          projectId: null,
          projectTracked: false,
          target: '/srv/work',
          reason: '30 folders',
        }),
        E(2, {
          at: '2025-10-30T09:00:00+07:00',
          finishedAt: null,
          action: 'project.stop',
          result: 'failed',
          reason: 'timed out',
        }),
      ),
    )
    open()
    const today = await screen.findByRole('list', { name: 'Today · 01/11' })
    const days = screen.getAllByRole('heading', { level: 2 }).map((h) => h.textContent)
    expect(days).toEqual(['Today · 01/11', 'Yesterday · 31/10', 'Thu 30/10'])

    const items = within(today).getAllByRole('listitem')
    expect(items).toHaveLength(2)
    expect(items[0]).toHaveTextContent('10:12')
    expect(items[0]).toHaveTextContent('dev@company.vn restarted shop')
    expect(items[0]).toHaveTextContent('12s')
    expect(items[0]).toHaveTextContent('Succeeded')
    expect(items[1]).toHaveTextContent('02:00')
    expect(items[1]).toHaveTextContent('Schedule backed up shop · shop_production')
    expect(items[1]).toHaveTextContent('84.2 MB · 41s')

    const older = within(screen.getByRole('list', { name: 'Thu 30/10' })).getByRole('listitem')
    expect(older).toHaveTextContent('stopped shop')
    expect(older).toHaveTextContent('timed out')
    expect(older).toHaveTextContent('Failed')
    expect(screen.getByRole('list', { name: 'Yesterday · 31/10' })).toHaveTextContent('Done')
  })

  it('counts a +07:00 entry at 00:30 as today although UTC says yesterday', async () => {
    vi.setSystemTime(new Date('2025-10-31T18:00:00Z'))
    serveActivity([E(1, { at: '2025-11-01T00:30:00+07:00', finishedAt: null })])
    open()
    expect(await screen.findByRole('list', { name: 'Today · 01/11' })).toHaveTextContent('00:30')
  })

  it('words an alert with its channels and a failed one in red', async () => {
    serveActivity(
      rows(
        E(2, {
          actor: 'system',
          actorKind: 'system',
          action: 'alert.project',
          kind: 'alert',
          target: 'blog',
          result: 'sent',
          reason: 'blog-wp-1 exited with code 1',
          channels: ['telegram', 'email'],
        }),
        E(1, {
          actor: 'system',
          actorKind: 'system',
          action: 'alert.backup',
          kind: 'alert',
          target: 'crm',
          result: 'failed',
          reason: 'Backup failed · Telegram: Could not reach Telegram.',
        }),
      ),
    )
    open()
    const [sent, failed] = await screen.findAllByRole('listitem')
    expect(sent).toHaveTextContent('System detected an error in blog')
    expect(sent).toHaveTextContent('blog-wp-1 exited with code 1 · Sent via Telegram, email')
    expect(sent).toHaveTextContent('Alert sent')
    expect(failed).toHaveTextContent('Not delivered')
    expect(within(failed).getByText(/Could not reach Telegram/).className).toMatch(
      /text-danger-text/,
    )
  })

  it('links a tracked target to its project and leaves an untracked one as text', async () => {
    serveActivity(
      rows(
        E(2, { projectId: 7, projectTracked: true, target: 'shop' }),
        E(1, { projectId: 8, projectTracked: false, target: 'old' }),
      ),
    )
    open()
    const link = await screen.findByRole('link', { name: 'shop' })
    expect(link).toHaveAttribute('href', '/projects/7')
    expect(screen.getByText('old')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'old' })).toBeNull()
  })

  it('shows the raw action for an unknown one', async () => {
    serveActivity([E(1, { action: 'legacy.thing', target: 'x', projectId: null })])
    open()
    expect(await screen.findByRole('listitem')).toHaveTextContent('dev@company.vn legacy.thing x')
  })
})

describe('ActivityPage filters', () => {
  it('sets ?kind= from the chips and reads again', async () => {
    const stub = serveActivity([E(1)])
    open('/activity?project=1')
    await screen.findByRole('listitem')
    const all = screen.getByRole('button', { name: 'All' })
    expect(all).toHaveAttribute('aria-pressed', 'true')

    fireEvent.click(screen.getByRole('button', { name: 'Backups' }))
    expect(screen.getByRole('button', { name: 'Backups' })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByRole('button', { name: 'All' })).toHaveAttribute('aria-pressed', 'false')
    await waitFor(() =>
      expect(activityCalls(stub)).toContain('/api/activity?kind=backup&project=1'),
    )

    fireEvent.click(screen.getByRole('button', { name: 'All' }))
    await waitFor(() => expect(activityCalls(stub).at(-1)).toBe('/api/activity?project=1'))
  })

  it('reads the filters from the URL on load', async () => {
    const stub = serveActivity([E(1)])
    open('/activity?kind=alert&project=3')
    await screen.findByRole('listitem')
    expect(activityCalls(stub)).toEqual(['/api/activity?kind=alert&project=3'])
    expect(screen.getByRole('button', { name: 'Alerts' })).toHaveAttribute('aria-pressed', 'true')
  })

  it('lists the tracked projects in the Project select and sets ?project=', async () => {
    const stub = serveActivity([E(1)])
    open('/activity', [
      makeProject({ id: 2, displayName: 'shop' }),
      makeProject({ id: 5, displayName: 'blog' }),
    ])
    await screen.findByRole('listitem')
    const select = screen.getByRole('combobox', { name: 'Project' })
    expect(
      within(select)
        .getAllByRole('option')
        .map((o) => o.textContent),
    ).toEqual(['All', 'blog', 'shop'])

    fireEvent.change(select, { target: { value: '5' } })
    await waitFor(() => expect(activityCalls(stub).at(-1)).toBe('/api/activity?project=5'))
    expect(select).toHaveValue('5')
  })
})

describe('ActivityPage paging and refresh', () => {
  const newest = (n: number) =>
    Array.from({ length: n }, (_, i) => E(100 - i, { target: `p${100 - i}`, projectId: null }))

  it('appends the next page on Show older and stops the 30 s refresh', async () => {
    const stub = serveActivity([], (url) =>
      url.includes('before=')
        ? json(200, { entries: [E(50, { target: 'older', projectId: null })], more: false })
        : json(200, { entries: newest(2), more: true }),
    )
    open()
    await screen.findAllByRole('listitem')
    expect(screen.getAllByRole('listitem')).toHaveLength(2)

    fireEvent.click(screen.getByRole('button', { name: 'Show older' }))
    await screen.findByText('older')
    expect(screen.getAllByRole('listitem')).toHaveLength(3)
    expect(activityCalls(stub)).toEqual(['/api/activity', '/api/activity?before=99'])
    expect(screen.queryByRole('button', { name: 'Show older' })).toBeNull()

    await act(async () => {
      vi.advanceTimersByTime(90_000)
    })
    expect(activityCalls(stub)).toHaveLength(2)
    expect(screen.getAllByRole('listitem')).toHaveLength(3)
  })

  it('reads the first page again every 30 s while no older page is loaded', async () => {
    let n = 0
    const stub = serveActivity([], () => {
      n++
      return json(200, { entries: [E(n, { target: `read${n}`, projectId: null })], more: false })
    })
    open()
    await screen.findByText('read1')
    await act(async () => {
      vi.advanceTimersByTime(30_000)
    })
    await screen.findByText('read2')
    expect(screen.queryByText('read1')).toBeNull()
    expect(activityCalls(stub)).toHaveLength(2)
  })

  it('shows an error on Show older and keeps the rows', async () => {
    serveActivity([], (url) =>
      url.includes('before=')
        ? json(500, { error: 'boom' })
        : json(200, { entries: newest(1), more: true }),
    )
    open()
    await screen.findByRole('listitem')
    fireEvent.click(screen.getByRole('button', { name: 'Show older' }))
    expect(await screen.findByText('boom')).toBeInTheDocument()
    expect(screen.getAllByRole('listitem')).toHaveLength(1)
    expect(screen.getByRole('button', { name: 'Show older' })).toBeEnabled()
  })
})

describe('ActivityPage error then Show older', () => {
  it('clears the refresh error and the dim when Show older succeeds, keeping the rows', async () => {
    let fail = false
    serveActivity([], (url) => {
      if (url.includes('before=')) {
        return json(200, { entries: [E(50, { target: 'older', projectId: null })], more: false })
      }
      return fail
        ? json(500, { error: 'boom' })
        : json(200, { entries: [E(99, { projectId: null })], more: true })
    })
    open()
    await screen.findByRole('listitem')
    fail = true
    await act(async () => {
      vi.advanceTimersByTime(30_000)
    })
    await screen.findByRole('alert')
    expect(screen.getByTestId('activity-days').className).toMatch(/opacity-55/)

    fireEvent.click(screen.getByRole('button', { name: 'Show older' }))
    await screen.findByText('older')
    await waitFor(() => expect(screen.queryByRole('alert')).toBeNull())
    expect(screen.getByTestId('activity-days').className).not.toMatch(/opacity-55/)
    expect(screen.getAllByRole('listitem')).toHaveLength(2)
  })
})

describe('ActivityPage empty and error states', () => {
  it('says there is no activity yet', async () => {
    serveActivity([])
    open()
    expect(await screen.findByText('No activity yet.')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Clear filters' })).toBeNull()
  })

  it('says nothing matches the filters, and Clear filters removes them', async () => {
    const stub = serveActivity([], (url) =>
      url.includes('kind=')
        ? json(200, { entries: [], more: false })
        : json(200, { entries: [E(1)], more: false }),
    )
    const r = open('/activity?kind=alert&project=2')
    expect(await screen.findByText('Nothing matches these filters.')).toBeInTheDocument()
    expect(screen.queryByText('No activity yet.')).toBeNull()

    fireEvent.click(screen.getByRole('button', { name: 'Clear filters' }))
    await screen.findByRole('listitem')
    expect(r.router.state.location.search).toBe('')
    expect(activityCalls(stub).at(-1)).toBe('/api/activity')
  })

  it('shows a LoadError with Retry when the first read fails', async () => {
    let fail = true
    serveActivity([], () =>
      fail ? json(500, { error: 'boom' }) : json(200, { entries: [E(1)], more: false }),
    )
    open()
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Could not load the activity.')
    expect(alert).toHaveTextContent('boom')
    expect(screen.queryByRole('status', { name: 'Loading' })).toBeNull()

    fail = false
    fireEvent.click(within(alert).getByRole('button', { name: 'Retry' }))
    await screen.findByRole('listitem')
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('keeps the shown rows, dimmed, when a refresh fails, and Retry reads again', async () => {
    let fail = false
    serveActivity([], () =>
      fail ? json(500, { error: 'boom' }) : json(200, { entries: [E(1)], more: false }),
    )
    open()
    await screen.findByRole('listitem')

    fail = true
    await act(async () => {
      vi.advanceTimersByTime(30_000)
    })
    const alert = await screen.findByRole('alert')
    expect(screen.getAllByRole('listitem')).toHaveLength(1)
    expect(screen.getByTestId('activity-days').className).toMatch(/opacity-55/)

    fail = false
    fireEvent.click(within(alert).getByRole('button', { name: 'Retry' }))
    await waitFor(() => expect(screen.queryByRole('alert')).toBeNull())
    expect(screen.getByTestId('activity-days').className).not.toMatch(/opacity-55/)
  })
})
