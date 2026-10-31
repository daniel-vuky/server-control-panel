import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { useLocation } from 'react-router'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { FakeEventSource } from '../../test/fakeEventSource'
import { pushState } from '../../test/projects'
import { renderApp } from '../../test/renderApp'
import { GB, makeSeries, makeServerInfo, makeUsage, makeUsageRow } from '../../test/server'
import ServerPage from './ServerPage'

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status })

type Answers = {
  server?: () => Response
  series?: (range: string) => Response
  usage?: () => Response
}

/** serve answers the three reads of the page; every call is kept in the returned stub. */
function serve(a: Answers = {}) {
  const stub = vi.fn(async (url: string) => {
    if (url === '/api/server') return a.server?.() ?? json(200, makeServerInfo())
    if (url.startsWith('/api/server/metrics?range=')) {
      const range = url.slice(url.indexOf('=') + 1)
      return a.series?.(range) ?? json(200, makeSeries({ range }))
    }
    if (url === '/api/server/usage') return a.usage?.() ?? json(200, makeUsage([makeUsageRow()]))
    return json(404, { error: `unrouted ${url}` })
  })
  vi.stubGlobal('fetch', stub)
  return stub
}

/** calls are the URLs fetched at path, with any query. */
const calls = (stub: ReturnType<typeof vi.fn>, path: string) =>
  stub.mock.calls.map((c) => c[0] as string).filter((u) => u === path || u.startsWith(`${path}?`))

function Where() {
  const loc = useLocation()
  return <div data-testid="where">{`${loc.pathname}${loc.search}`}</div>
}

const renderPage = (path = '/server') =>
  renderApp(path, {
    routes: [
      {
        path: '/server',
        element: (
          <>
            <ServerPage />
            <Where />
          </>
        ),
      },
    ],
  })

beforeEach(() => {
  FakeEventSource.instances = []
  vi.stubGlobal('EventSource', FakeEventSource)
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('ServerPage', () => {
  it('shows the heading and loading before the answers', () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => new Promise(() => {})),
    )
    renderPage()
    expect(screen.getByRole('heading', { level: 1, name: 'Server' })).toBeInTheDocument()
    expect(screen.getByText('Loading the server…')).toBeInTheDocument()
  })

  it("holds the charts' place with skeleton cards while a period loads", async () => {
    serve({ series: () => new Promise<Response>(() => {}) as unknown as Response })
    renderPage()
    const section = await screen.findByRole('region', { name: 'Resource charts' })
    expect(within(section).getAllByRole('status', { name: 'Loading' })).toHaveLength(4)
  })

  it("holds the usage table's place with a skeleton card while it loads", async () => {
    const stub = serve({ usage: () => new Promise<Response>(() => {}) as unknown as Response })
    renderPage()
    await screen.findByRole('region', { name: 'Resource charts' })
    expect(screen.getByRole('status', { name: 'Loading' })).toBeInTheDocument()
    expect(calls(stub, '/api/server/usage')).toHaveLength(1)
  })

  it('has the header line with hostname, cores, RAM, root disk and the sampling', async () => {
    serve()
    renderPage()
    expect(
      await screen.findByText(
        'prod-01 · 4 vCPU · 8.0 GB RAM · 160 GB disk · sampled every 30 seconds',
      ),
    ).toBeInTheDocument()
  })

  it('names the root disk of several', async () => {
    serve({
      server: () =>
        json(
          200,
          makeServerInfo({
            disks: [
              { mount: '/var/lib/docker', usedBytes: 1, totalBytes: 500 * GB },
              { mount: '/', usedBytes: 1, totalBytes: 160 * GB },
            ],
          }),
        ),
    })
    renderPage()
    expect(await screen.findByText(/· 160 GB disk ·/)).toBeInTheDocument()
  })

  it('names the fallback disk, and — for what nothing has answered', async () => {
    serve({
      server: () =>
        json(
          200,
          makeServerInfo({
            hostname: '',
            cores: 0,
            ramBytes: 0,
            disks: [{ mount: '/ (panel data)', usedBytes: 1, totalBytes: 40 * GB }],
            disksPartial: true,
          }),
        ),
    })
    renderPage()
    expect(
      await screen.findByText('— · — vCPU · — RAM · 40 GB disk · sampled every 30 seconds'),
    ).toBeInTheDocument()
  })

  it('reads 24 hours by default and all four charts re-read on a new period', async () => {
    const stub = serve()
    renderPage()
    const group = await screen.findByRole('group', { name: 'Time range' })
    expect(within(group).getByRole('button', { name: '24 hours' })).toHaveAttribute(
      'aria-pressed',
      'true',
    )
    expect(await screen.findByRole('img', { name: 'CPU chart, last 24 hours' })).toBeInTheDocument()
    expect(calls(stub, '/api/server/metrics')).toEqual(['/api/server/metrics?range=24h'])

    fireEvent.click(within(group).getByRole('button', { name: '7 days' }))
    expect(within(group).getByRole('button', { name: '7 days' })).toHaveAttribute(
      'aria-pressed',
      'true',
    )
    expect(await screen.findByRole('img', { name: 'CPU chart, last 7 days' })).toBeInTheDocument()
    for (const name of [
      'RAM chart, last 7 days',
      'Disk I/O chart, Read and Write, last 7 days',
      'Network chart, In and Out, last 7 days',
    ]) {
      expect(screen.getByRole('img', { name })).toBeInTheDocument()
    }
    expect(calls(stub, '/api/server/metrics').at(-1)).toBe('/api/server/metrics?range=7d')
    expect(screen.getByTestId('where')).toHaveTextContent('/server?range=7d')
  })

  it('honours ?range=7d', async () => {
    const stub = serve()
    renderPage('/server?range=7d')
    expect(await screen.findByRole('img', { name: 'CPU chart, last 7 days' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '7 days' })).toHaveAttribute('aria-pressed', 'true')
    expect(calls(stub, '/api/server/metrics')).toEqual(['/api/server/metrics?range=7d'])
  })

  it('shows the charts, usage, system info and services', async () => {
    serve()
    renderPage()
    expect(await screen.findByRole('region', { name: 'Resource charts' })).toBeInTheDocument()
    expect(await screen.findByRole('heading', { name: 'Usage by app' })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'System info' })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'System services' })).toBeInTheDocument()
  })

  it('says server metrics are not available without the host /proc', async () => {
    serve({
      server: () =>
        json(
          200,
          makeServerInfo({
            available: false,
            reason: "Server metrics are not available: the host's /proc is not mounted.",
          }),
        ),
    })
    renderPage()
    expect(
      await screen.findByText("Server metrics are not available: the host's /proc is not mounted."),
    ).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: 'Resource charts' })).toBeNull()
    // The usage by app still has the projects' numbers.
    expect(await screen.findByRole('heading', { name: 'Usage by app' })).toBeInTheDocument()
  })

  it('stops reading the series once the server says there are no server metrics', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const stub = serve({ server: () => json(200, makeServerInfo({ available: false })) })
    renderPage()
    expect(
      await screen.findByText("Server metrics are not available: the host's /proc is not mounted."),
    ).toBeInTheDocument()
    const first = calls(stub, '/api/server/metrics').length
    expect(first).toBeLessThanOrEqual(1)
    act(() => {
      vi.advanceTimersByTime(30_000)
    })
    await waitFor(() => expect(calls(stub, '/api/server')).toHaveLength(2))
    expect(calls(stub, '/api/server/metrics')).toHaveLength(first)
    expect(screen.queryByText('Could not load the charts.')).toBeNull()
  })

  it('follows the live Docker state at once', async () => {
    serve({
      usage: () => json(200, makeUsage([makeUsageRow({ cpuPercent: 12 })])),
    })
    renderPage()
    const services = await screen.findByRole('region', { name: 'System services' })
    await screen.findByRole('heading', { name: 'Usage by app' })
    expect(within(services).getByText('Running')).toBeInTheDocument()

    pushState([], false)
    expect(within(services).getByText('Not responding')).toBeInTheDocument()
    const table = screen.getByRole('table')
    expect(within(table).getAllByRole('cell')[0]).toHaveTextContent('—')
  })

  it('shows an error with Retry when the server cannot be read', async () => {
    let fail = true
    serve({
      server: () => (fail ? json(500, { error: 'boom' }) : json(200, makeServerInfo())),
    })
    renderPage()
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Could not load the server.')
    expect(alert).toHaveTextContent('boom')
    fail = false
    fireEvent.click(within(alert).getByRole('button', { name: 'Retry' }))
    expect(await screen.findByRole('region', { name: 'System info' })).toBeInTheDocument()
  })

  it('shows an error for the charts and for the usage on their own', async () => {
    serve({
      series: () => json(500, { error: 'no series' }),
      usage: () => json(500, { error: 'no usage' }),
    })
    renderPage()
    expect(await screen.findByText('Could not load the charts.')).toBeInTheDocument()
    expect(await screen.findByText('Could not load the usage by app.')).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'System info' })).toBeInTheDocument()
  })

  it('re-reads every 30 seconds while visible, keeping what it shows', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    let n = 0
    const stub = serve({
      usage: () => {
        n++
        return json(200, makeUsage([makeUsageRow({ name: n === 1 ? 'shop' : 'shop-2' })]))
      },
    })
    renderPage()
    expect(await screen.findByRole('link', { name: 'shop' })).toBeInTheDocument()
    expect(calls(stub, '/api/server/usage')).toHaveLength(1)

    act(() => {
      vi.advanceTimersByTime(30_000)
    })
    // The old rows stay until the new ones arrive (no loading flash).
    expect(screen.getByRole('table')).toBeInTheDocument()
    expect(await screen.findByRole('link', { name: 'shop-2' })).toBeInTheDocument()
    await waitFor(() => {
      expect(calls(stub, '/api/server')).toHaveLength(2)
      expect(calls(stub, '/api/server/metrics')).toHaveLength(2)
      expect(calls(stub, '/api/server/usage')).toHaveLength(2)
    })
  })

  it('keeps the last values when a refresh fails', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    let n = 0
    serve({
      usage: () => (++n === 1 ? json(200, makeUsage([makeUsageRow()])) : json(500, { error: 'x' })),
    })
    renderPage()
    expect(await screen.findByRole('link', { name: 'shop' })).toBeInTheDocument()
    act(() => {
      vi.advanceTimersByTime(30_000)
    })
    await waitFor(() => expect(n).toBe(2))
    expect(screen.getByRole('link', { name: 'shop' })).toBeInTheDocument()
    expect(screen.queryByText('Could not load the usage by app.')).toBeNull()
  })
})
