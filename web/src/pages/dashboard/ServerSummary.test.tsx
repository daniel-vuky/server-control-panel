import { act, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { makeSeries, makeServerInfo } from '../../test/server'
import ServerSummary from './ServerSummary'

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status })

type Answers = {
  server?: () => Response | Promise<Response>
  series?: () => Response | Promise<Response>
}

function serve(a: Answers = {}) {
  const stub = vi.fn(async (url: string) => {
    if (url === '/api/server') return a.server?.() ?? json(200, makeServerInfo())
    if (url === '/api/server/metrics?range=1h') {
      return a.series?.() ?? json(200, makeSeries({ range: '1h' }))
    }
    return json(404, { error: `unrouted ${url}` })
  })
  vi.stubGlobal('fetch', stub)
  return stub
}

const latestOf = () => {
  const l = makeSeries().latest
  if (!l) throw new Error('fixture has no latest')
  return l
}

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('ServerSummary', () => {
  it('shows six skeleton tiles until both reads answered, then the six labelled tiles', async () => {
    let answerSeries: (r: Response) => void = () => {}
    serve({ series: () => new Promise<Response>((r) => (answerSeries = r)) })
    const { container } = render(<ServerSummary />)
    const section = screen.getByRole('region', { name: 'Server summary' })
    expect(section.getAttribute('aria-busy')).toBe('true')
    expect(container.querySelectorAll('[data-skeleton-tile]').length).toBe(6)
    // info alone is not enough
    await act(async () => {
      await Promise.resolve()
    })
    expect(container.querySelectorAll('[data-skeleton-tile]').length).toBe(6)
    expect(screen.queryByText('Load avg')).toBeNull()

    await act(async () => answerSeries(json(200, makeSeries({ range: '1h' }))))
    await waitFor(() => expect(screen.getByText('Load avg')).toBeTruthy())
    expect(container.querySelectorAll('[data-skeleton-tile]').length).toBe(0)
    expect(section.getAttribute('aria-busy')).toBeNull()
    for (const name of ['CPU', 'RAM', 'Disk', 'Load avg', 'Uptime', 'CF Tunnel']) {
      expect(screen.getByRole('article', { name })).toBeTruthy()
    }
    const cpu = screen.getByRole('article', { name: 'CPU' })
    expect(within(cpu).getByText('34%')).toBeTruthy()
    expect(within(cpu).getByText('4 vCPU')).toBeTruthy()
    expect(
      within(screen.getByRole('article', { name: 'CF Tunnel' })).getByText('Connected'),
    ).toBeTruthy()
  })

  it('shows tiles with dashes when the metrics read fails but the facts answered', async () => {
    serve({ series: () => json(500, { error: 'boom' }) })
    render(<ServerSummary />)
    await waitFor(() => expect(screen.getByRole('article', { name: 'CPU' })).toBeTruthy())
    expect(within(screen.getByRole('article', { name: 'CPU' })).getByText('—')).toBeTruthy()
    expect(
      within(screen.getByRole('article', { name: 'CF Tunnel' })).getByText('Connected'),
    ).toBeTruthy()
  })

  it('shows tiles when the facts read fails but the metrics answered', async () => {
    serve({ server: () => json(500, { error: 'boom' }) })
    render(<ServerSummary />)
    await waitFor(() => expect(screen.getByRole('article', { name: 'CPU' })).toBeTruthy())
    expect(within(screen.getByRole('article', { name: 'CPU' })).getByText('34%')).toBeTruthy()
    expect(within(screen.getByRole('article', { name: 'Uptime' })).getByText('—')).toBeTruthy()
  })

  it('marks an over-threshold tile with the red edge and flag', async () => {
    serve({
      series: () => json(200, makeSeries({ range: '1h', latest: { ...latestOf(), cpu: 95 } })),
    })
    render(<ServerSummary />)
    const cpu = await screen.findByRole('article', { name: 'CPU' })
    expect(within(cpu).getByText('Over threshold')).toBeTruthy()
    expect(cpu.className).toContain('border-error-border')
  })

  it('re-reads both every 30 seconds while visible', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const stub = serve()
    render(<ServerSummary />)
    await waitFor(() => expect(screen.getByRole('article', { name: 'CPU' })).toBeTruthy())
    expect(stub).toHaveBeenCalledTimes(2)
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000)
    })
    await waitFor(() => expect(stub).toHaveBeenCalledTimes(4))
    const urls = stub.mock.calls.map((c) => c[0])
    expect(urls.filter((u) => u === '/api/server').length).toBe(2)
    expect(urls.filter((u) => u === '/api/server/metrics?range=1h').length).toBe(2)
  })
})
