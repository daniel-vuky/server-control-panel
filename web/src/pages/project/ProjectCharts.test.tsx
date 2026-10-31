import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { BackupSettings, ProjectPoint, ProjectSeries } from '../../api/types.gen'
import { FakeEventSource } from '../../test/fakeEventSource'
import { makeProject, pushState, renderProject } from '../../test/projects'

const MB = 1024 * 1024

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status })

const settings: BackupSettings = {
  defaults: { frequency: 'daily', time: '02:00', weekday: 0, keep: 14, compress: true },
  timeZone: { name: 'Asia/Ho_Chi_Minh', offset: '+07:00' },
}

const point = (t: string, v: Partial<ProjectPoint> = {}): ProjectPoint => ({
  t,
  cpu: 12,
  mem: 600 * MB,
  netIn: 1 * MB,
  netOut: 0.5 * MB,
  ...v,
})

/** series is a 1-hour project series of three 30 s buckets, with a fresh latest sample; overrides win. */
function series(overrides: Partial<ProjectSeries> = {}): ProjectSeries {
  return {
    range: '1h',
    stepSeconds: 30,
    from: '2026-10-31T09:59:00Z',
    to: '2026-10-31T10:00:00Z',
    historyStart: '2026-10-25T00:00:00Z',
    latest: {
      at: '2026-10-31T10:00:10Z',
      cpu: 55,
      mem: 900 * MB,
      netIn: 1.2 * MB,
      netOut: 0.6 * MB,
    },
    points: [
      point('2026-10-31T09:59:00Z'),
      point('2026-10-31T09:59:30Z', { cpu: 14 }),
      point('2026-10-31T10:00:00Z', { cpu: 10 }),
    ],
    ...overrides,
  }
}

/** serve answers the project's series (per range) and the server zone; every call is kept. */
function serve(answer: (range: string) => Response = (range) => json(200, series({ range }))) {
  const stub = vi.fn(async (url: string) => {
    if (url.startsWith('/api/projects/1/metrics?range=')) {
      return answer(url.slice(url.indexOf('=') + 1))
    }
    if (url === '/api/backup-settings') return json(200, settings)
    return json(404, { error: `unrouted ${url}` })
  })
  vi.stubGlobal('fetch', stub)
  return stub
}

const seriesCalls = (stub: ReturnType<typeof vi.fn>) =>
  stub.mock.calls.map((c) => c[0] as string).filter((u) => u.startsWith('/api/projects/1/metrics'))

const live = makeProject({ cpuPercent: 23.4, memBytes: 412 * MB })

const region = () => screen.getByRole('region', { name: 'App resources' })
const chart = (title: string) => screen.getByTestId(`chart-${title}`)

beforeEach(() => {
  FakeEventSource.instances = []
  vi.stubGlobal('EventSource', FakeEventSource)
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('project resource charts', () => {
  it('has App resources with the range switch, 1 hour by default, and three charts', async () => {
    const stub = serve()
    renderProject('/projects/1')
    pushState([live])
    expect(within(region()).getByRole('heading', { name: 'App resources' })).toBeInTheDocument()
    expect(within(region()).getByRole('button', { name: '1 hour' })).toHaveAttribute(
      'aria-pressed',
      'true',
    )
    expect(await screen.findByRole('img', { name: 'CPU chart, last 1 hour' })).toBeInTheDocument()
    expect(screen.getByRole('img', { name: 'RAM chart, last 1 hour' })).toBeInTheDocument()
    expect(
      screen.getByRole('img', { name: 'Network chart, In and Out, last 1 hour' }),
    ).toBeInTheDocument()
    expect(seriesCalls(stub)).toEqual(['/api/projects/1/metrics?range=1h'])
    // Smaller than the Server page's charts (design 02).
    expect(screen.getByRole('img', { name: 'CPU chart, last 1 hour' })).toHaveAttribute(
      'viewBox',
      '0 0 600 130',
    )
  })

  it('sits above the Containers card', async () => {
    serve()
    renderProject('/projects/1')
    pushState([live])
    const containers = screen.getByRole('region', { name: 'Containers' })
    expect(
      region().compareDocumentPosition(containers) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    await screen.findByRole('img', { name: 'CPU chart, last 1 hour' })
  })

  it('keeps the range in the URL and reads that period', async () => {
    const stub = serve()
    const { router } = renderProject('/projects/1?range=24h')
    pushState([live])
    expect(within(region()).getByRole('button', { name: '24 hours' })).toHaveAttribute(
      'aria-pressed',
      'true',
    )
    expect(await screen.findByRole('img', { name: 'CPU chart, last 24 hours' })).toBeInTheDocument()

    fireEvent.click(within(region()).getByRole('button', { name: '7 days' }))
    expect(await screen.findByRole('img', { name: 'CPU chart, last 7 days' })).toBeInTheDocument()
    expect(router.state.location.search).toBe('?range=7d')
    expect(seriesCalls(stub)).toEqual([
      '/api/projects/1/metrics?range=24h',
      '/api/projects/1/metrics?range=7d',
    ])
  })

  it('CPU and RAM now come from the live project, not the series', async () => {
    serve()
    renderProject('/projects/1')
    pushState([live])
    await screen.findByRole('img', { name: 'CPU chart, last 1 hour' })
    expect(chart('CPU')).toHaveTextContent('23%')
    expect(chart('CPU')).not.toHaveTextContent('55%')
    expect(chart('RAM')).toHaveTextContent('412 MB')
    expect(chart('RAM')).not.toHaveTextContent('900 MB')

    pushState([{ ...live, cpuPercent: 180, memBytes: 2 * 1024 * MB }])
    expect(chart('CPU')).toHaveTextContent('180%')
    expect(chart('RAM')).toHaveTextContent('2.0 GB')

    pushState([{ ...live, cpuPercent: null, memBytes: null }])
    expect(within(chart('CPU')).getByText('—')).toBeInTheDocument()
    expect(within(chart('RAM')).getByText('—')).toBeInTheDocument()
  })

  it('Network has In solid and Out dashed, and sums the latest sample', async () => {
    serve()
    renderProject('/projects/1')
    pushState([live])
    await screen.findByRole('img', { name: 'Network chart, In and Out, last 1 hour' })
    const net = chart('Network')
    expect(net).toHaveTextContent('1.8 MB/s')
    expect(within(net).getByText('in + out')).toBeInTheDocument()
    expect(net.querySelector('polyline[data-series="In"]')).not.toHaveAttribute('stroke-dasharray')
    expect(net.querySelector('polyline[data-series="Out"]')).toHaveAttribute(
      'stroke-dasharray',
      '4 3',
    )
  })

  it('Network reads — without a latest sample', async () => {
    serve(() => json(200, series({ latest: null })))
    renderProject('/projects/1')
    pushState([live])
    await screen.findByRole('img', { name: 'Network chart, In and Out, last 1 hour' })
    expect(within(chart('Network')).getByText('—')).toBeInTheDocument()
  })

  it('leaves a gap where the project was stopped', async () => {
    serve(() =>
      json(
        200,
        series({
          points: [
            point('2026-10-31T09:58:30Z'),
            point('2026-10-31T09:59:00Z'),
            point('2026-10-31T09:59:30Z', { cpu: null, mem: null, netIn: null, netOut: null }),
            point('2026-10-31T10:00:00Z'),
            point('2026-10-31T10:00:30Z'),
          ],
        }),
      ),
    )
    renderProject('/projects/1')
    pushState([live])
    await screen.findByRole('img', { name: 'CPU chart, last 1 hour' })
    expect(chart('CPU').querySelectorAll('polyline[data-series="CPU"]')).toHaveLength(2)
    expect(chart('RAM').querySelectorAll('polyline')).toHaveLength(2)
    expect(chart('Network').querySelectorAll('polyline[data-series="Out"]')).toHaveLength(2)
  })

  it('says where the history begins, in the server zone', async () => {
    serve(() => json(200, series({ historyStart: '2026-10-31T09:59:30Z' })))
    renderProject('/projects/1')
    pushState([live])
    // 09:59:30 UTC is 16:59 in Asia/Ho_Chi_Minh.
    expect(await within(region()).findAllByText('History begins Sat 31 Oct, 16:59')).toHaveLength(3)
  })

  it('shows a load error with a retry', async () => {
    let fail = true
    serve((range) => (fail ? json(500, { error: 'boom' }) : json(200, series({ range }))))
    renderProject('/projects/1')
    pushState([live])
    expect(await within(region()).findByText('Could not load the charts.')).toBeInTheDocument()
    fail = false
    fireEvent.click(within(region()).getByRole('button', { name: 'Retry' }))
    expect(await screen.findByRole('img', { name: 'CPU chart, last 1 hour' })).toBeInTheDocument()
  })

  it('re-reads the series every 30 s', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const stub = serve()
    renderProject('/projects/1')
    pushState([live])
    await screen.findByRole('img', { name: 'CPU chart, last 1 hour' })
    expect(seriesCalls(stub)).toHaveLength(1)
    vi.advanceTimersByTime(30_000)
    await waitFor(() => expect(seriesCalls(stub)).toHaveLength(2))
  })
})
