import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { LiveState } from '../api/types.gen'
import { FakeEventSource } from '../test/fakeEventSource'
import { LiveProvider, useLive } from './live'

function Probe() {
  const { dockerUp, projects, backups, batch, drive, checking, recheckDocker, connection } =
    useLive()
  return (
    <div>
      <span data-testid="connection">{connection}</span>
      <span data-testid="docker">{String(dockerUp)}</span>
      <span data-testid="projects">{projects === null ? 'null' : projects.length}</span>
      <span data-testid="backups">{backups.length}</span>
      <span data-testid="drive">{drive === null ? 'null' : drive.state}</span>
      <span data-testid="batch">{batch === null ? 'null' : `${batch.done}/${batch.total}`}</span>
      <span data-testid="checking">{String(checking)}</span>
      <button type="button" onClick={() => void recheckDocker()}>
        recheck
      </button>
    </div>
  )
}

const state: LiveState = {
  dockerUp: true,
  backups: [],
  batch: null,
  drive: { state: 'connected', email: 'ops@example.com', folder: null, reason: '' },
  projects: [
    {
      id: 1,
      displayName: 'shop',
      path: '/w/shop',
      workspaceId: 1,
      self: false,
      status: 'running',
      actions: ['stop'],
      databases: [],
      missingSince: '',
      publicDomain: '',
      stopTemplate: 'stop',
      alertCpuPercent: null,
      alertRamMb: null,
      composeFiles: ['compose.yaml'],
      cpuPercent: null,
      memBytes: null,
      upSince: '',
      containers: [],
      driveFolder: 'shop',
    },
  ],
}

describe('LiveProvider', () => {
  beforeEach(() => {
    FakeEventSource.instances = []
    vi.stubGlobal('EventSource', FakeEventSource)
  })
  afterEach(() => vi.unstubAllGlobals())

  it('is null until the first state event, then holds the state', () => {
    render(
      <LiveProvider>
        <Probe />
      </LiveProvider>,
    )
    expect(FakeEventSource.instances).toHaveLength(1)
    expect(FakeEventSource.instances[0].url).toBe('/api/events')
    expect(screen.getByTestId('docker')).toHaveTextContent('null')
    expect(screen.getByTestId('projects')).toHaveTextContent('null')

    act(() => FakeEventSource.instances[0].emit('state', state))
    expect(screen.getByTestId('docker')).toHaveTextContent('true')
    expect(screen.getByTestId('projects')).toHaveTextContent('1')

    act(() => FakeEventSource.instances[0].emit('state', { dockerUp: false, projects: [] }))
    expect(screen.getByTestId('docker')).toHaveTextContent('false')
    expect(screen.getByTestId('projects')).toHaveTextContent('0')
  })

  it('exposes the Drive status and the active backups', () => {
    render(
      <LiveProvider>
        <Probe />
      </LiveProvider>,
    )
    expect(screen.getByTestId('drive')).toHaveTextContent('null')
    expect(screen.getByTestId('backups')).toHaveTextContent('0')
    act(() =>
      FakeEventSource.instances[0].emit('state', {
        ...state,
        backups: [{ id: 7 }],
        drive: { ...state.drive, state: 'expired' },
      }),
    )
    expect(screen.getByTestId('drive')).toHaveTextContent('expired')
    expect(screen.getByTestId('backups')).toHaveTextContent('1')
  })

  it('defaults backups to none and drive to null when the state lacks them', () => {
    render(
      <LiveProvider>
        <Probe />
      </LiveProvider>,
    )
    act(() => FakeEventSource.instances[0].emit('state', state))
    expect(screen.getByTestId('drive')).toHaveTextContent('connected')
    act(() => FakeEventSource.instances[0].emit('state', { dockerUp: true, projects: [] }))
    expect(screen.getByTestId('drive')).toHaveTextContent('null')
    expect(screen.getByTestId('backups')).toHaveTextContent('0')
  })

  it('exposes the latest Back up all batch, null when the state has none', () => {
    render(
      <LiveProvider>
        <Probe />
      </LiveProvider>,
    )
    expect(screen.getByTestId('batch')).toHaveTextContent('null')
    const batch = {
      id: 3,
      startedBy: 'ops@example.com',
      startedAt: '2026-10-31T10:00:00Z',
      total: 6,
      done: 2,
      ok: 2,
      failed: 0,
      skipped: 1,
      finishedAt: null,
    }
    act(() => FakeEventSource.instances[0].emit('state', { ...state, batch }))
    expect(screen.getByTestId('batch')).toHaveTextContent('2/6')
    act(() => FakeEventSource.instances[0].emit('state', { ...state, batch: null }))
    expect(screen.getByTestId('batch')).toHaveTextContent('null')
    act(() => FakeEventSource.instances[0].emit('state', { ...state, batch }))
    // An older server sends no batch field at all.
    act(() => FakeEventSource.instances[0].emit('state', { dockerUp: true, projects: [] }))
    expect(screen.getByTestId('batch')).toHaveTextContent('null')
  })

  it('ignores a malformed state event', () => {
    render(
      <LiveProvider>
        <Probe />
      </LiveProvider>,
    )
    act(() => FakeEventSource.instances[0].emit('state', 'not json'))
    act(() => FakeEventSource.instances[0].emit('state', { nope: 1 }))
    expect(screen.getByTestId('docker')).toHaveTextContent('null')
  })

  it('closes the source on unmount', () => {
    const { unmount } = render(
      <LiveProvider>
        <Probe />
      </LiveProvider>,
    )
    const es = FakeEventSource.instances[0]
    expect(es.closed).toBe(false)
    unmount()
    expect(es.closed).toBe(true)
  })

  it('does nothing when EventSource is missing', () => {
    vi.stubGlobal('EventSource', undefined)
    render(
      <LiveProvider>
        <Probe />
      </LiveProvider>,
    )
    expect(screen.getByTestId('docker')).toHaveTextContent('null')
  })

  it('recheckDocker posts the check, tracks checking and takes the answer', async () => {
    let resolve: (r: Response) => void = () => {}
    const fetchMock = vi.fn(() => new Promise<Response>((r) => (resolve = r)))
    vi.stubGlobal('fetch', fetchMock)
    render(
      <LiveProvider>
        <Probe />
      </LiveProvider>,
    )
    act(() => FakeEventSource.instances[0].emit('state', { dockerUp: false, projects: [] }))
    fireEvent.click(screen.getByRole('button', { name: 'recheck' }))
    expect(screen.getByTestId('checking')).toHaveTextContent('true')
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/docker/check',
      expect.objectContaining({ method: 'POST' }),
    )
    resolve(new Response('{"dockerUp":true}', { status: 200 }))
    await waitFor(() => expect(screen.getByTestId('checking')).toHaveTextContent('false'))
    expect(screen.getByTestId('docker')).toHaveTextContent('true')
  })

  it('recheckDocker before any state keeps projects null', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response('{"dockerUp":true}', { status: 200 })),
    )
    render(
      <LiveProvider>
        <Probe />
      </LiveProvider>,
    )
    fireEvent.click(screen.getByRole('button', { name: 'recheck' }))
    await waitFor(() => expect(screen.getByTestId('docker')).toHaveTextContent('true'))
    expect(screen.getByTestId('projects')).toHaveTextContent('null')
  })

  it('recheckDocker keeps dockerUp as it was when the check fails', async () => {
    // The setup stub answers 401, so postJSON throws an ApiError.
    render(
      <LiveProvider>
        <Probe />
      </LiveProvider>,
    )
    act(() => FakeEventSource.instances[0].emit('state', { dockerUp: false, projects: [] }))
    fireEvent.click(screen.getByRole('button', { name: 'recheck' }))
    await waitFor(() => expect(screen.getByTestId('checking')).toHaveTextContent('false'))
    expect(screen.getByTestId('docker')).toHaveTextContent('false')
  })

  it('recheckDocker survives a network error', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        throw new TypeError('network down')
      }),
    )
    render(
      <LiveProvider>
        <Probe />
      </LiveProvider>,
    )
    act(() => FakeEventSource.instances[0].emit('state', { dockerUp: false, projects: [] }))
    fireEvent.click(screen.getByRole('button', { name: 'recheck' }))
    await waitFor(() => expect(screen.getByTestId('checking')).toHaveTextContent('false'))
    expect(screen.getByTestId('docker')).toHaveTextContent('false')
  })
})

describe('LiveProvider connection', () => {
  const EVENTS = '/api/events'
  const latest = () => {
    const es = FakeEventSource.latest(EVENTS)
    if (!es) throw new Error('no EventSource for /api/events')
    return es
  }
  const connection = () => screen.getByTestId('connection')
  const renderProbe = () =>
    render(
      <LiveProvider>
        <Probe />
      </LiveProvider>,
    )
  /** advance moves the fake clock and lets the timers' callbacks and promises settle. */
  const advance = (ms: number) =>
    act(async () => {
      await vi.advanceTimersByTimeAsync(ms)
    })
  /** closeLatest makes the current source CLOSED and lets the provider react. */
  const closeLatest = () =>
    act(async () => {
      latest().closeWithError()
      await vi.advanceTimersByTimeAsync(0)
    })

  beforeEach(() => {
    FakeEventSource.instances = []
    vi.stubGlobal('EventSource', FakeEventSource)
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('opens then shows Realtime', () => {
    renderProbe()
    expect(connection()).toHaveTextContent('connecting')
    act(() => latest().open())
    // Open counts from the first state, which carries the current values.
    expect(connection()).toHaveTextContent('connecting')
    act(() => latest().emit('state', state))
    expect(connection()).toHaveTextContent('open')
  })

  it('stays connecting on an error before the first state', () => {
    renderProbe()
    act(() => latest().fail())
    expect(connection()).toHaveTextContent('connecting')
  })

  it('error after open shows lost and keeps the last values', () => {
    renderProbe()
    act(() => latest().emit('state', state))
    act(() => latest().fail())
    expect(connection()).toHaveTextContent('lost')
    expect(screen.getByTestId('projects')).toHaveTextContent('1')
    expect(screen.getByTestId('docker')).toHaveTextContent('true')
    // The browser retries a CONNECTING source on its own.
    expect(FakeEventSource.forUrl(EVENTS)).toHaveLength(1)
  })

  it('recovers on the next state event', () => {
    renderProbe()
    act(() => latest().emit('state', state))
    act(() => latest().fail())
    expect(connection()).toHaveTextContent('lost')
    act(() => latest().open())
    expect(connection()).toHaveTextContent('lost')
    act(() => latest().emit('state', { dockerUp: false, projects: [] }))
    expect(connection()).toHaveTextContent('open')
    expect(screen.getByTestId('projects')).toHaveTextContent('0')
  })

  it('recreates a closed EventSource with backoff', async () => {
    vi.useFakeTimers()
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response('{"email":"dev@localhost"}', { status: 200 })),
    )
    renderProbe()
    act(() => latest().emit('state', state))

    // Delays 1 s, 2 s, 5 s, then every 10 s.
    for (const [n, delay] of [
      [2, 1000],
      [3, 2000],
      [4, 5000],
      [5, 10000],
      [6, 10000],
    ]) {
      const before = latest()
      await closeLatest()
      expect(before.closed).toBe(true)
      expect(connection()).toHaveTextContent('lost')
      await advance(delay - 1)
      expect(FakeEventSource.forUrl(EVENTS)).toHaveLength(n - 1)
      await advance(1)
      expect(FakeEventSource.forUrl(EVENTS)).toHaveLength(n)
    }

    // A state on the new source resets the backoff.
    act(() => latest().emit('state', state))
    expect(connection()).toHaveTextContent('open')
    await closeLatest()
    await advance(1000)
    expect(FakeEventSource.forUrl(EVENTS)).toHaveLength(7)
  })

  it('detects an expired session after 3 closed attempts', async () => {
    vi.useFakeTimers()
    const fetchMock = vi.fn(async () => new Response('{"error":"unauthorized"}', { status: 401 }))
    vi.stubGlobal('fetch', fetchMock)
    renderProbe()
    act(() => latest().emit('state', state))

    await closeLatest()
    await advance(1000)
    await closeLatest()
    await advance(2000)
    expect(fetchMock).not.toHaveBeenCalled()
    expect(connection()).toHaveTextContent('lost')

    await closeLatest()
    expect(fetchMock).toHaveBeenCalledWith('/api/me', { redirect: 'manual' })
    expect(connection()).toHaveTextContent('expired')

    // An expired session is not retried: Reload is the way back.
    await advance(60000)
    expect(FakeEventSource.forUrl(EVENTS)).toHaveLength(3)
  })

  it.each([
    [
      'an opaque redirect',
      () => {
        const r = new Response(null)
        Object.defineProperty(r, 'type', { value: 'opaqueredirect' })
        return r
      },
    ],
    ['a 403', () => new Response('', { status: 403 })],
  ])('treats %s from /api/me as expired', async (_name, answer) => {
    vi.useFakeTimers()
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => answer()),
    )
    renderProbe()
    await closeLatest()
    await advance(1000)
    await closeLatest()
    await advance(2000)
    await closeLatest()
    expect(connection()).toHaveTextContent('expired')
  })

  it('keeps retrying every 10 s while /api/me answers or fails', async () => {
    vi.useFakeTimers()
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response('{"email":"dev@localhost"}', { status: 200 }))
      .mockRejectedValueOnce(new TypeError('network down'))
    vi.stubGlobal('fetch', fetchMock)
    renderProbe()
    act(() => latest().emit('state', state))
    await closeLatest()
    await advance(1000)
    await closeLatest()
    await advance(2000)
    await closeLatest()
    expect(connection()).toHaveTextContent('lost')
    await advance(5000)
    expect(FakeEventSource.forUrl(EVENTS)).toHaveLength(4)
    await closeLatest()
    expect(fetchMock).toHaveBeenCalledTimes(2)
    expect(connection()).toHaveTextContent('lost')
    await advance(10000)
    expect(FakeEventSource.forUrl(EVENTS)).toHaveLength(5)
  })

  it('a source that goes silent for 60 s shows Connection lost and opens a new source', async () => {
    vi.useFakeTimers()
    const fetchMock = vi.fn(async () => new Response('{"error":"unauthorized"}', { status: 401 }))
    vi.stubGlobal('fetch', fetchMock)
    renderProbe()
    const first = latest()
    act(() => first.emit('state', state))
    await advance(59_999)
    expect(connection()).toHaveTextContent('open')
    expect(first.closed).toBe(false)

    await advance(1)
    expect(connection()).toHaveTextContent('lost')
    expect(first.closed).toBe(true)
    expect(screen.getByTestId('projects')).toHaveTextContent('1')
    await advance(999)
    expect(FakeEventSource.forUrl(EVENTS)).toHaveLength(1)
    await advance(1)
    expect(FakeEventSource.forUrl(EVENTS)).toHaveLength(2)
    act(() => latest().emit('state', state))
    expect(connection()).toHaveTextContent('open')

    // A stall is not a closed attempt: three stalls never ask about the session.
    for (const n of [3, 4, 5]) {
      await advance(60_000 + 1000)
      expect(FakeEventSource.forUrl(EVENTS)).toHaveLength(n)
    }
    expect(fetchMock).not.toHaveBeenCalled()
    expect(connection()).toHaveTextContent('lost')
  })

  it('a silent source before any state stays connecting and reopens', async () => {
    vi.useFakeTimers()
    renderProbe()
    await advance(60_000)
    expect(connection()).toHaveTextContent('connecting')
    expect(FakeEventSource.forUrl(EVENTS)[0].closed).toBe(true)
    await advance(1000)
    expect(FakeEventSource.forUrl(EVENTS)).toHaveLength(2)
  })

  it('a ping within the window keeps Realtime', async () => {
    vi.useFakeTimers()
    renderProbe()
    act(() => latest().emit('state', state))
    await advance(50_000)
    act(() => latest().emit('ping', {}))
    await advance(50_000)
    expect(connection()).toHaveTextContent('open')
    expect(FakeEventSource.forUrl(EVENTS)).toHaveLength(1)
    expect(latest().closed).toBe(false)
    await advance(10_000)
    expect(connection()).toHaveTextContent('lost')
  })

  it('clears the watchdog when the source closes and on unmount', async () => {
    vi.useFakeTimers()
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response('{"email":"dev@localhost"}', { status: 200 })),
    )
    const { unmount } = renderProbe()
    act(() => latest().emit('state', state))
    await advance(30_000)
    await closeLatest()
    // The closed source's watchdog is gone: only the backoff reopens it.
    await advance(1000)
    expect(FakeEventSource.forUrl(EVENTS)).toHaveLength(2)
    await advance(30_000)
    expect(FakeEventSource.forUrl(EVENTS)).toHaveLength(2)
    unmount()
    await advance(120_000)
    expect(FakeEventSource.forUrl(EVENTS)).toHaveLength(2)
  })

  it('stops reconnecting on unmount', async () => {
    vi.useFakeTimers()
    const { unmount } = renderProbe()
    act(() => latest().emit('state', state))
    await closeLatest()
    unmount()
    await advance(60000)
    expect(FakeEventSource.forUrl(EVENTS)).toHaveLength(1)
  })
})
