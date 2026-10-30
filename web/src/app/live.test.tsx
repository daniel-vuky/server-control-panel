import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { LiveState } from '../api/types.gen'
import { FakeEventSource } from '../test/fakeEventSource'
import { LiveProvider, useLive } from './live'

function Probe() {
  const { dockerUp, projects, checking, recheckDocker } = useLive()
  return (
    <div>
      <span data-testid="docker">{String(dockerUp)}</span>
      <span data-testid="projects">{projects === null ? 'null' : projects.length}</span>
      <span data-testid="checking">{String(checking)}</span>
      <button type="button" onClick={() => void recheckDocker()}>
        recheck
      </button>
    </div>
  )
}

const state: LiveState = {
  dockerUp: true,
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
