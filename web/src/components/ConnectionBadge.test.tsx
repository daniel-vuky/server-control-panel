import { act, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { LiveProvider } from '../app/live'
import { FakeEventSource } from '../test/fakeEventSource'
import { makeProject } from '../test/projects'
import { renderApp } from '../test/renderApp'
import { holdShellReads } from '../test/server'
import ConnectionBadge from './ConnectionBadge'

const EVENTS = '/api/events'
const LOST = 'Connection lost, retrying…'

const latest = () => {
  const es = FakeEventSource.latest(EVENTS)
  if (!es) throw new Error('no EventSource for /api/events')
  return es
}
const ready = () => act(() => latest().emit('state', { dockerUp: true, projects: [] }))

/** content is the wrapper around the page body that dims while the values are stale. */
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

/** headerOf is the page header of the h1 with that name (a header inside main has no banner role). */
const headerOf = (name: string) => {
  const header = screen.getByRole('heading', { level: 1, name }).closest('header')
  if (!(header instanceof HTMLElement)) throw new Error(`no header around "${name}"`)
  return header
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
  beforeEach(holdShellReads)

  it.each([
    ['/', 'Dashboard'],
    ['/backups', 'Backups'],
  ])('shows the badge in the header of %s, outside the dimmed content', (path, title) => {
    renderApp(path)
    ready()
    const badge = screen.getByText('Realtime')
    expect(headerOf(title)).toContainElement(badge)
    expect(content()).not.toContainElement(badge)
    expectNotDimmed()
  })

  it('error after open shows lost and dims the page body, not its header', () => {
    renderApp('/backups')
    ready()
    act(() => latest().fail())
    const badge = screen.getByText(LOST)
    expect(headerOf('Backups')).toContainElement(badge)
    expect(content()).not.toContainElement(badge)
    expectDimmed()
    // The page's content is what dims, not the sidebar or the header.
    const nav = screen.getByRole('navigation', { name: 'Main navigation' })
    expect(content()).not.toContainElement(nav)
    expect(content()).not.toContainElement(
      screen.getByRole('heading', { level: 1, name: 'Backups' }),
    )
  })

  it('keeps the Docker banner bright while the content dims', () => {
    renderApp('/backups')
    act(() => latest().emit('state', { dockerUp: false, projects: [] }))
    act(() => latest().fail())
    expectDimmed()
    const banner = screen.getByText('Docker daemon is not responding.')
    expect(screen.getByRole('main')).toContainElement(banner)
    expect(content()).not.toContainElement(banner)
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

  it('shows the badge on the onboarding page, in the brand row beside the card title', () => {
    renderApp('/onboarding')
    ready()
    expect(screen.getByRole('main')).toContainElement(screen.getByText('Realtime'))
    expect(content()).not.toContainElement(screen.getByText('Realtime'))
    expect(
      screen.getByRole('heading', { level: 1, name: 'Add project folder' }),
    ).toBeInTheDocument()
  })

  it('shows the badge on the rescan page', () => {
    renderApp('/rescan/3')
    ready()
    expect(screen.getByRole('main')).toContainElement(screen.getByText('Realtime'))
    expect(content()).not.toContainElement(screen.getByText('Realtime'))
    expect(screen.getByRole('heading', { level: 1, name: 'Rescan' })).toBeInTheDocument()
  })

  it('shows the badge on the Not found page', () => {
    renderApp('/nope')
    ready()
    expect(screen.getByRole('main')).toContainElement(screen.getByText('Realtime'))
    expect(content()).not.toContainElement(screen.getByText('Realtime'))
    expect(screen.getByRole('heading', { level: 1, name: 'Page not found' })).toBeInTheDocument()
  })

  it('shows the badge on the project page, in the breadcrumb row', () => {
    renderApp('/projects/1')
    act(() => latest().emit('state', { dockerUp: true, projects: [makeProject()] }))
    const badge = screen.getByText('Realtime')
    const crumbs = screen.getByRole('navigation', { name: 'Breadcrumb' })
    expect(crumbs.parentElement).toContainElement(badge)
    expect(content()).not.toContainElement(badge)
    expect(content()).toContainElement(screen.getByTestId('project-header'))
  })

  it('shows the badge on the project page while the project is not tracked', () => {
    renderApp('/projects/9')
    act(() => latest().emit('state', { dockerUp: true, projects: [] }))
    expect(screen.getByText('This project is not tracked.')).toBeInTheDocument()
    expect(screen.getByText('Realtime')).toBeInTheDocument()
    expect(content()).not.toContainElement(screen.getByText('Realtime'))
  })
})

const workspace = {
  id: 1,
  name: 'work',
  path: '/srv/work',
  depth: 3,
  lastScanAt: '2026-10-30T09:42:00Z',
  tracked: 1,
  ignored: 0,
}

/** stubApi answers the reads of the Dashboard and Settings; any other path is a 404. */
function stubApi(shop: ReturnType<typeof makeProject>) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      const body: Record<string, unknown> = {
        '/api/projects': [shop],
        '/api/workspaces': [workspace],
      }
      return url in body
        ? new Response(JSON.stringify(body[url]), { status: 200 })
        : new Response('{"error":"unrouted"}', { status: 404 })
    }),
  )
}

describe('overlays while the connection is lost', () => {
  it('keeps the Dashboard confirm dialog bright when the connection is lost while it is open', async () => {
    const shop = makeProject()
    stubApi(shop)
    renderApp('/')
    act(() => latest().emit('state', { dockerUp: true, projects: [shop] }))
    const card = await screen.findByRole('article', { name: 'shop' })
    fireEvent.click(within(card).getByRole('switch', { name: 'Stop shop' }))
    const dialog = screen.getByRole('dialog')

    act(() => latest().fail())
    expectDimmed()
    expect(screen.getByRole('dialog')).toBe(dialog)
    expect(content()).not.toContainElement(dialog)
    expect(dialog.closest('[data-live-content]')).toBeNull()
  })

  it('keeps the Settings remove-workspace confirm outside the dimmed content', async () => {
    stubApi(makeProject())
    renderApp('/settings')
    ready()
    fireEvent.click(await screen.findByRole('button', { name: 'Remove /srv/work' }))
    const dialog = screen.getByRole('dialog')
    act(() => latest().fail())
    expectDimmed()
    expect(dialog.closest('[data-live-content]')).toBeNull()
  })

  it('keeps the open ⋯ menu outside the dimmed content, closing on an outside click only', async () => {
    const shop = makeProject()
    stubApi(shop)
    renderApp('/')
    act(() => latest().emit('state', { dockerUp: true, projects: [shop] }))
    const card = await screen.findByRole('article', { name: 'shop' })
    fireEvent.click(within(card).getByRole('button', { name: 'More actions for shop' }))
    act(() => latest().fail())
    expectDimmed()
    const menu = screen.getByRole('menu', { name: 'Actions for shop' })
    expect(menu.closest('[data-live-content]')).toBeNull()

    // A click inside the menu (on its label, not an item) leaves it open.
    fireEvent.mouseDown(menu)
    expect(screen.getByRole('menu', { name: 'Actions for shop' })).toBeInTheDocument()
    // A click outside closes it.
    fireEvent.mouseDown(screen.getByRole('main'))
    expect(screen.queryByRole('menu')).toBeNull()
  })
})
