import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { FakeEventSource } from '../test/fakeEventSource'
import { renderApp } from '../test/renderApp'

const renderSettings = () => renderApp('/settings')

const NOT_RESPONDING = 'Docker daemon is not responding.'

// The Settings page raises its own alert and Retry for a failed load (the
// setup fetch stub answers 401), so find the banner by its text.
const banner = () => {
  const el = screen.getByText(NOT_RESPONDING).closest('[role="alert"]')
  if (!(el instanceof HTMLElement)) throw new Error('banner is not an alert')
  return el
}
const noBanner = () => expect(screen.queryByText(NOT_RESPONDING)).toBeNull()
const retry = () => within(banner()).getByRole('button', { name: 'Retry' })

describe('DockerBanner', () => {
  beforeEach(() => {
    FakeEventSource.instances = []
    vi.stubGlobal('EventSource', FakeEventSource)
  })
  afterEach(() => vi.unstubAllGlobals())

  it('is hidden until Docker is reported down', () => {
    renderSettings()
    noBanner()
    act(() => FakeEventSource.instances[0].emit('state', { dockerUp: true, projects: [] }))
    noBanner()
  })

  it('shows the alert while Docker is down and clears when it is back', () => {
    renderSettings()
    act(() => FakeEventSource.instances[0].emit('state', { dockerUp: false, projects: [] }))
    const alert = banner()
    expect(alert).toHaveTextContent(NOT_RESPONDING)
    expect(alert).toHaveTextContent('Start/Stop for Docker projects is locked.')
    expect(alert).toHaveTextContent('systemctl status docker')
    act(() => FakeEventSource.instances[0].emit('state', { dockerUp: true, projects: [] }))
    noBanner()
  })

  it('Retry posts the check and is disabled while it runs', async () => {
    let resolve: (r: Response) => void = () => {}
    const fetchMock = vi.fn((url: string) =>
      url === '/api/docker/check'
        ? new Promise<Response>((r) => (resolve = r))
        : Promise.resolve(new Response('{"error":"unauthorized"}', { status: 401 })),
    )
    vi.stubGlobal('fetch', fetchMock)
    renderSettings()
    act(() => FakeEventSource.instances[0].emit('state', { dockerUp: false, projects: [] }))
    const retryButton = retry()
    expect(retryButton).toBeEnabled()
    fireEvent.click(retryButton)
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/docker/check',
      expect.objectContaining({ method: 'POST' }),
    )
    expect(retry()).toBeDisabled()
    resolve(new Response('{"dockerUp":false}', { status: 200 }))
    await waitFor(() => expect(retry()).toBeEnabled())
    expect(banner()).toBeInTheDocument()
  })

  it('a failing check leaves the banner and re-enables Retry', async () => {
    renderSettings()
    act(() => FakeEventSource.instances[0].emit('state', { dockerUp: false, projects: [] }))
    fireEvent.click(retry())
    await waitFor(() => expect(retry()).toBeEnabled())
    expect(banner()).toBeInTheDocument()
  })
})
