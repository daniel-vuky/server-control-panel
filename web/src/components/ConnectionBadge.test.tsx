import { act, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { LiveProvider } from '../app/live'
import { FakeEventSource } from '../test/fakeEventSource'
import { renderApp } from '../test/renderApp'
import ConnectionBadge from './ConnectionBadge'

const EVENTS = '/api/events'
const LOST = 'Connection lost, retrying…'

const latest = () => {
  const es = FakeEventSource.latest(EVENTS)
  if (!es) throw new Error('no EventSource for /api/events')
  return es
}
const ready = () => act(() => latest().emit('state', { dockerUp: true, projects: [] }))

/** content is the wrapper around the page that dims while the values are stale. */
const content = () => {
  const el = screen.getByRole('main').querySelector('[data-live-content]')
  if (!(el instanceof HTMLElement)) throw new Error('no live content wrapper')
  return el
}
const expectDimmed = () => {
  expect(content()).toHaveStyle({ opacity: '0.55', filter: 'saturate(0.6)' })
}
const expectNotDimmed = () => {
  expect(content().style.opacity).toBe('')
  expect(content().style.filter).toBe('')
}

/** closeThrice makes three sources in a row CLOSED, which leads to the /api/me check. */
async function closeThrice() {
  for (const delay of [1000, 2000, 0]) {
    await act(async () => {
      latest().closeWithError()
      await vi.advanceTimersByTimeAsync(delay)
    })
  }
}

beforeEach(() => {
  FakeEventSource.instances = []
  vi.stubGlobal('EventSource', FakeEventSource)
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('ConnectionBadge', () => {
  const renderBadge = (onReload?: () => void) =>
    render(
      <LiveProvider>
        <ConnectionBadge onReload={onReload} />
      </LiveProvider>,
    )

  it('shows nothing while the first connection opens', () => {
    renderBadge()
    act(() => latest().open())
    expect(screen.queryByText('Realtime')).toBeNull()
    expect(screen.queryByText(LOST)).toBeNull()
  })

  it('opens then shows Realtime', () => {
    renderBadge()
    ready()
    expect(screen.getByText('Realtime')).toBeInTheDocument()
    expect(screen.queryByText(LOST)).toBeNull()
  })

  it('shows Connection lost while the source retries, Realtime once a state is back', () => {
    renderBadge()
    ready()
    act(() => latest().fail())
    expect(screen.getByText(LOST)).toBeInTheDocument()
    expect(screen.queryByText('Realtime')).toBeNull()
    act(() => latest().open())
    expect(screen.getByText(LOST)).toBeInTheDocument()
    ready()
    expect(screen.getByText('Realtime')).toBeInTheDocument()
  })

  it('shows Session expired with Reload after 3 closed attempts', async () => {
    vi.useFakeTimers()
    const onReload = vi.fn()
    renderBadge(onReload)
    ready()
    // The setup fetch stub answers 401 to /api/me.
    await closeThrice()
    expect(screen.getByText('Session expired')).toBeInTheDocument()
    expect(screen.queryByText(LOST)).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Reload' }))
    expect(onReload).toHaveBeenCalledOnce()
  })
})

describe('app shell connection state', () => {
  it('shows the badge in the content area and does not dim while open', () => {
    renderApp('/backups')
    ready()
    const main = screen.getByRole('main')
    expect(main).toContainElement(screen.getByText('Realtime'))
    expectNotDimmed()
  })

  it('error after open shows lost and dims main', () => {
    renderApp('/backups')
    ready()
    act(() => latest().fail())
    expect(screen.getByText(LOST)).toBeInTheDocument()
    expectDimmed()
    // The page itself is what dims, not the sidebar.
    const nav = screen.getByRole('navigation', { name: 'Main navigation' })
    expect(content()).not.toContainElement(nav)
    expect(content()).toContainElement(screen.getByRole('heading', { level: 1, name: 'Backups' }))
  })

  it('recovers on the next state event', () => {
    renderApp('/backups')
    ready()
    act(() => latest().fail())
    expectDimmed()
    ready()
    expectNotDimmed()
    expect(screen.getByText('Realtime')).toBeInTheDocument()
  })

  it('badge sits outside the dimmed element', () => {
    renderApp('/backups')
    ready()
    act(() => latest().fail())
    const badge = screen.getByText(LOST)
    expect(screen.getByRole('main')).toContainElement(badge)
    expect(content()).not.toContainElement(badge)
  })

  it('dims while the session is expired', async () => {
    vi.useFakeTimers()
    renderApp('/backups')
    ready()
    await closeThrice()
    expect(screen.getByText('Session expired')).toBeInTheDocument()
    expectDimmed()
  })

  it('does not dim before the first state', () => {
    renderApp('/backups')
    expectNotDimmed()
  })
})
