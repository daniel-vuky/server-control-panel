import { act, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Backup, LatestBackups, LiveState } from '../api/types.gen'
import { FakeEventSource } from '../test/fakeEventSource'
import { LatestBackupsProvider, useLatestBackups } from './latestBackups'
import { LiveProvider } from './live'

const latestBody = (state: string): LatestBackups => {
  const row = {
    id: 3,
    projectId: 1,
    projectName: 'shop',
    database: 'shop',
    state,
    at: '2026-10-31T02:00:00Z',
  }
  return { latest: row, projects: [row] }
}

function Probe() {
  const { data, error } = useLatestBackups()
  return (
    <div>
      <span data-testid="data">{data === null ? 'null' : data.projects[0].state}</span>
      <span data-testid="error">{String(error)}</span>
    </div>
  )
}

const live = (backups: Partial<Backup>[]): LiveState =>
  ({ dockerUp: true, projects: [], backups, batch: null, drive: null }) as unknown as LiveState

const emit = (s: LiveState) => {
  const es = FakeEventSource.latest('/api/events')
  if (!es) throw new Error('no EventSource')
  act(() => es.emit('state', s))
}

describe('LatestBackupsProvider', () => {
  let body: LatestBackups | 'fail'
  let fetchMock: ReturnType<typeof vi.fn>
  const reads = () => fetchMock.mock.calls.filter((c) => c[0] === '/api/backups/latest').length

  beforeEach(() => {
    FakeEventSource.instances = []
    vi.stubGlobal('EventSource', FakeEventSource)
    body = latestBody('ok')
    fetchMock = vi.fn(async (url: string) => {
      if (url === '/api/backups/latest') {
        if (body === 'fail') return new Response('{"error":"x"}', { status: 500 })
        return new Response(JSON.stringify(body), { status: 200 })
      }
      return new Response('{}', { status: 401 })
    })
    vi.stubGlobal('fetch', fetchMock)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  const renderProvider = () =>
    render(
      <LiveProvider>
        <LatestBackupsProvider>
          <Probe />
        </LatestBackupsProvider>
      </LiveProvider>,
    )

  it('reads once the first live state arrives, not before', async () => {
    renderProvider()
    expect(reads()).toBe(0)
    emit(live([]))
    await waitFor(() => expect(screen.getByTestId('data')).toHaveTextContent('ok'))
    expect(reads()).toBe(1)
  })

  it('reads again when a live backup changes state, not on mere progress', async () => {
    renderProvider()
    emit(live([{ id: 4, state: 'queued' }]))
    await waitFor(() => expect(reads()).toBe(1))

    emit(live([{ id: 4, state: 'queued', progress: 10 } as Partial<Backup>]))
    await act(async () => {})
    expect(reads()).toBe(1)

    body = latestBody('failed')
    emit(live([{ id: 4, state: 'running' }]))
    await waitFor(() => expect(reads()).toBe(2))
    await waitFor(() => expect(screen.getByTestId('data')).toHaveTextContent('failed'))
  })

  it('keeps the last answer when a re-read fails', async () => {
    renderProvider()
    emit(live([]))
    await waitFor(() => expect(screen.getByTestId('data')).toHaveTextContent('ok'))
    body = 'fail'
    emit(live([{ id: 4, state: 'running' }]))
    await waitFor(() => expect(reads()).toBe(2))
    expect(screen.getByTestId('data')).toHaveTextContent('ok')
    expect(screen.getByTestId('error')).toHaveTextContent('false')
  })

  it('reports an error only while there is no data', async () => {
    body = 'fail'
    renderProvider()
    emit(live([]))
    await waitFor(() => expect(screen.getByTestId('error')).toHaveTextContent('true'))
    expect(screen.getByTestId('data')).toHaveTextContent('null')
  })

  it('answers no data and no error without a provider', () => {
    render(<Probe />)
    expect(screen.getByTestId('data')).toHaveTextContent('null')
    expect(screen.getByTestId('error')).toHaveTextContent('false')
  })
})
