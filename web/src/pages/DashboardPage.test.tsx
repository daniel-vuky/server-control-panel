import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Project } from '../api/types.gen'
import { LiveProvider } from '../app/live'
import { ToastProvider } from '../components/Toast'
import { FakeEventSource } from '../test/fakeEventSource'
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
      { type: 'postgresql', name: 'shop', locationKind: 'container', location: 'db', sources: [] },
      { type: 'sqlite', name: 'cache', locationKind: 'file', location: './cache.db', sources: [] },
    ],
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
  },
]

/** serve stubs fetch with one answer per call, the last one repeating. */
function serve(...answers: Array<[number, unknown]>) {
  let n = 0
  const fetchStub = vi.fn(async () => {
    const [status, body] = answers[Math.min(n++, answers.length - 1)]
    return new Response(JSON.stringify(body), { status })
  })
  vi.stubGlobal('fetch', fetchStub)
  return fetchStub
}

const renderPage = () =>
  render(
    <MemoryRouter>
      <ToastProvider>
        <LiveProvider>
          <DashboardPage />
        </LiveProvider>
      </ToastProvider>
    </MemoryRouter>,
  )

beforeEach(() => {
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
    expect(fetchStub.mock.calls[0]).toEqual(['/api/projects', expect.anything()])
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
      'Database',
      'Start / Stop',
    ])
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
    expect(fetchStub).toHaveBeenCalledTimes(2)
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
    function serveActions(next: () => Promise<Response> | Response) {
      const fetchStub = vi.fn(async (_url: string, init?: RequestInit) => {
        if (init?.method === 'POST') return next()
        return new Response(JSON.stringify(list), { status: 200 })
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
      expect(await screen.findByRole('status')).toHaveTextContent('Stopped shop')
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
      expect(await screen.findByRole('status')).toHaveTextContent('Started blog')
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
      const stub = vi.fn(async (_url: string, init?: RequestInit) => {
        if (init?.method === 'POST') throw new TypeError('Failed to fetch')
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
})
