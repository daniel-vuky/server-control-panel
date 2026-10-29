import { afterEach, describe, expect, it, vi } from 'vitest'
import { getScan, startScan, trackScan, watchScan } from './scans'

type Listener = (e: MessageEvent) => void

class StubEventSource {
  static instances: StubEventSource[] = []
  url: string
  closed = false
  onerror: (() => void) | null = null
  private listeners = new Map<string, Listener[]>()
  constructor(url: string) {
    this.url = url
    StubEventSource.instances.push(this)
  }
  addEventListener(type: string, fn: Listener) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), fn])
  }
  close() {
    this.closed = true
  }
  emit(type: string, data: unknown) {
    for (const fn of this.listeners.get(type) ?? []) {
      fn(new MessageEvent(type, { data: JSON.stringify(data) }))
    }
  }
  fail() {
    for (const fn of this.listeners.get('error') ?? []) fn(new MessageEvent('error'))
    this.onerror?.()
  }
}

const job = (state: string) => ({
  id: 3,
  state,
  path: '/srv',
  depth: 1,
  progress: { examined: 2, total: 2, current: '' },
  error: '',
})

const stubFetchJSON = (status: number, body: unknown) =>
  vi.fn(async () => new Response(JSON.stringify(body), { status }))

const flush = () => new Promise((r) => setTimeout(r, 0))

afterEach(() => {
  StubEventSource.instances = []
})

describe('watchScan', () => {
  it('delivers progress then end, then closes', () => {
    vi.stubGlobal('EventSource', StubEventSource)
    const progress = vi.fn()
    const end = vi.fn()
    watchScan(3, { progress, end })
    const es = StubEventSource.instances[0]
    expect(es.url).toBe('/api/scans/3/events')

    es.emit('progress', { examined: 1, total: 4, current: '/srv/a' })
    expect(progress).toHaveBeenCalledWith({ examined: 1, total: 4, current: '/srv/a' })

    es.emit('end', { state: 'done' })
    expect(end).toHaveBeenCalledWith('done')
    expect(es.closed).toBe(true)
  })

  it('on error with the job done, ends with done and closes', async () => {
    vi.stubGlobal('EventSource', StubEventSource)
    const fetchMock = stubFetchJSON(200, job('done'))
    vi.stubGlobal('fetch', fetchMock)
    const end = vi.fn()
    watchScan(3, { progress: vi.fn(), end })
    const es = StubEventSource.instances[0]

    es.fail()
    await flush()

    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(end).toHaveBeenCalledExactlyOnceWith('done')
    expect(es.closed).toBe(true)
  })

  it('on error with the job still running, keeps the source open and does not end', async () => {
    vi.stubGlobal('EventSource', StubEventSource)
    vi.stubGlobal('fetch', stubFetchJSON(200, job('running')))
    const end = vi.fn()
    watchScan(3, { progress: vi.fn(), end })
    const es = StubEventSource.instances[0]

    es.fail()
    await flush()

    expect(end).not.toHaveBeenCalled()
    expect(es.closed).toBe(false)
  })

  it('does not end twice when end arrives and then an error', async () => {
    vi.stubGlobal('EventSource', StubEventSource)
    vi.stubGlobal('fetch', stubFetchJSON(200, job('done')))
    const end = vi.fn()
    watchScan(3, { progress: vi.fn(), end })
    const es = StubEventSource.instances[0]

    es.emit('end', { state: 'done' })
    es.fail()
    await flush()

    expect(end).toHaveBeenCalledTimes(1)
  })

  it('returns a stop function that closes the source and silences callbacks', async () => {
    vi.stubGlobal('EventSource', StubEventSource)
    vi.stubGlobal('fetch', stubFetchJSON(200, job('done')))
    const progress = vi.fn()
    const end = vi.fn()
    const stop = watchScan(3, { progress, end })
    const es = StubEventSource.instances[0]

    stop()
    expect(es.closed).toBe(true)
    es.emit('progress', { examined: 1, total: 1, current: '' })
    es.fail()
    await flush()
    expect(progress).not.toHaveBeenCalled()
    expect(end).not.toHaveBeenCalled()
  })
})

describe('scan requests', () => {
  it('startScan posts to /api/scans', async () => {
    const fetchMock = stubFetchJSON(202, { id: 9 })
    vi.stubGlobal('fetch', fetchMock)
    await expect(startScan({ path: '/srv', depth: 2 })).resolves.toEqual({ id: 9 })
    expect((fetchMock.mock.calls[0] as unknown[])[0]).toBe('/api/scans')
  })

  it('getScan reads /api/scans/{id}', async () => {
    const fetchMock = stubFetchJSON(200, job('running'))
    vi.stubGlobal('fetch', fetchMock)
    await expect(getScan(3)).resolves.toMatchObject({ id: 3, state: 'running' })
    expect((fetchMock.mock.calls[0] as unknown[])[0]).toBe('/api/scans/3')
  })

  it('trackScan posts to /api/scans/{id}/track', async () => {
    const fetchMock = stubFetchJSON(200, { workspaceId: 1, added: 2 })
    vi.stubGlobal('fetch', fetchMock)
    await expect(trackScan(3, { projects: [] })).resolves.toEqual({ workspaceId: 1, added: 2 })
    expect((fetchMock.mock.calls[0] as unknown[])[0]).toBe('/api/scans/3/track')
  })
})
