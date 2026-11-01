import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { LatestBackup } from '../api/types.gen'
import { FakeEventSource } from '../test/fakeEventSource'
import { makeProject } from '../test/projects'
import { renderApp } from '../test/renderApp'
import { makeServerInfo } from '../test/server'

type Latest = Pick<LatestBackup, 'projectId' | 'state'>

const row = (l: Latest): LatestBackup => ({
  id: l.projectId,
  projectId: l.projectId,
  projectName: `p${l.projectId}`,
  database: 'db',
  state: l.state,
  at: '2026-10-31T02:00:00Z',
})

function stub(latest: Latest[] | 'fail', server: 'ok' | 'fail' = 'ok') {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      if (url === '/api/backups/latest') {
        if (latest === 'fail') return new Response('{"error":"x"}', { status: 500 })
        const projects = latest.map(row)
        return new Response(JSON.stringify({ latest: projects[0] ?? null, projects }), {
          status: 200,
        })
      }
      if (url === '/api/server') {
        if (server === 'fail') return new Response('{"error":"x"}', { status: 500 })
        return new Response(JSON.stringify(makeServerInfo()), { status: 200 })
      }
      return new Response('{"error":"unauthorized"}', { status: 401 })
    }),
  )
}

const ready = (n = 2) => {
  const es = FakeEventSource.latest('/api/events')
  if (!es) throw new Error('no EventSource')
  act(() =>
    es.emit('state', {
      dockerUp: true,
      projects: Array.from({ length: n }, (_, i) => makeProject({ id: i + 1 })),
    }),
  )
}

const nav = () => screen.getByRole('navigation', { name: 'Main navigation' })

beforeEach(() => {
  FakeEventSource.instances = []
  vi.stubGlobal('EventSource', FakeEventSource)
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('nav indicators', () => {
  it('shows the tracked project count on Projects once the live state arrives', async () => {
    stub([])
    renderApp('/')
    const link = within(nav()).getByRole('link', { name: /^Projects/ })
    expect(link).toHaveTextContent(/^Projects$/)
    ready(3)
    expect(link).toHaveTextContent(/^Projects3$/)
    await act(async () => {})
  })

  it('lights the Backups dot for a project whose latest backup is not ok', async () => {
    stub([
      { projectId: 1, state: 'failed' },
      { projectId: 2, state: 'ok' },
    ])
    renderApp('/')
    ready()
    expect(await within(nav()).findByLabelText('1 failed backup')).toBeInTheDocument()
  })

  it('counts refused and interrupted as failed and words the plural', async () => {
    stub([
      { projectId: 1, state: 'refused' },
      { projectId: 2, state: 'interrupted' },
      { projectId: 3, state: 'ok' },
    ])
    renderApp('/')
    ready()
    expect(await within(nav()).findByLabelText('2 failed backups')).toBeInTheDocument()
  })

  it('shows no dot when every latest backup is ok', async () => {
    stub([{ projectId: 1, state: 'ok' }])
    renderApp('/')
    ready()
    await waitFor(() =>
      expect(vi.mocked(fetch).mock.calls.some((c) => c[0] === '/api/backups/latest')).toBe(true),
    )
    await act(async () => {})
    expect(within(nav()).queryByLabelText(/failed backup/)).toBeNull()
  })

  it('shows no dot when the read fails', async () => {
    stub('fail')
    renderApp('/')
    ready()
    await act(async () => {})
    expect(within(nav()).queryByLabelText(/failed backup/)).toBeNull()
  })
})

describe('brand line', () => {
  it('shows hostname and OS once /api/server answers', async () => {
    stub([])
    renderApp('/')
    expect(await within(nav()).findByText('prod-01 · Ubuntu 24.04 LTS')).toBeInTheDocument()
  })

  it('shows nothing when /api/server fails', async () => {
    stub([], 'fail')
    renderApp('/')
    await act(async () => {})
    expect(within(nav()).queryByText(/·/)).toBeNull()
  })

  it('reads /api/server once at mount, not on every live state', async () => {
    stub([])
    renderApp('/onboarding')
    ready()
    await within(nav()).findByText('prod-01 · Ubuntu 24.04 LTS')
    ready()
    const calls = vi.mocked(fetch).mock.calls.filter((c) => c[0] === '/api/server')
    expect(calls.length).toBe(1)
  })
})

describe('phone menu', () => {
  const opener = () => screen.getByRole('button', { name: 'Open menu' })

  it('has a 44 by 44 Open menu button that shows below 768 px, and hides the sidebar there', () => {
    stub([])
    renderApp('/')
    expect(opener()).toHaveClass('size-11')
    expect(opener().parentElement).toHaveClass('md:hidden')
    expect(nav()).toHaveClass('max-md:hidden')
    expect(screen.queryByRole('dialog', { name: 'Menu' })).toBeNull()
  })

  it('opens the same links in a drawer dialog', async () => {
    stub([])
    renderApp('/')
    fireEvent.click(opener())
    const drawer = screen.getByRole('dialog', { name: 'Menu' })
    const wide = within(nav())
      .getAllByRole('link')
      .map((a) => [a.textContent, a.getAttribute('href')])
    const links = within(drawer)
      .getAllByRole('link')
      .map((a) => [a.textContent, a.getAttribute('href')])
    expect(links).toEqual(wide)
    expect(drawer).toHaveAttribute('aria-modal', 'true')
    await act(async () => {})
  })

  it('closes on Escape, on the overlay and on a link click', async () => {
    stub([])
    renderApp('/')
    fireEvent.click(opener())
    fireEvent.keyDown(screen.getByRole('dialog', { name: 'Menu' }), { key: 'Escape' })
    expect(screen.queryByRole('dialog', { name: 'Menu' })).toBeNull()

    fireEvent.click(opener())
    fireEvent.click(screen.getByTestId('confirm-overlay'))
    expect(screen.queryByRole('dialog', { name: 'Menu' })).toBeNull()

    fireEvent.click(opener())
    fireEvent.click(
      within(screen.getByRole('dialog', { name: 'Menu' })).getByRole('link', { name: /^Backups/ }),
    )
    expect(screen.queryByRole('dialog', { name: 'Menu' })).toBeNull()
    await act(async () => {})
  })

  it('reads /api/me once: opening the drawer shows the same user without a new read', async () => {
    stub([])
    const base = vi.mocked(fetch).getMockImplementation()
    vi.mocked(fetch).mockImplementation(async (url, init) =>
      url === '/api/me'
        ? new Response('{"email":"admin@company.vn"}', { status: 200 })
        : (base as typeof fetch)(url, init),
    )
    renderApp('/')
    await within(nav()).findByText('admin@company.vn')
    fireEvent.click(opener())
    const drawer = screen.getByRole('dialog', { name: 'Menu' })
    expect(within(drawer).getByText('admin@company.vn')).toBeInTheDocument()
    fireEvent.keyDown(drawer, { key: 'Escape' })
    fireEvent.click(opener())
    await act(async () => {})
    expect(vi.mocked(fetch).mock.calls.filter((c) => c[0] === '/api/me')).toHaveLength(1)
  })

  it('closes when the window is widened to 768 px or more', async () => {
    const listeners = new Set<() => void>()
    let wide = false
    vi.stubGlobal(
      'matchMedia',
      vi.fn((query: string) => ({
        get matches() {
          return query === '(min-width: 768px)' && wide
        },
        media: query,
        addEventListener: (_: string, fn: () => void) => {
          if (query === '(min-width: 768px)') listeners.add(fn)
        },
        removeEventListener: (_: string, fn: () => void) => listeners.delete(fn),
      })),
    )
    stub([])
    renderApp('/')
    fireEvent.click(opener())
    expect(screen.getByRole('dialog', { name: 'Menu' })).toBeInTheDocument()
    act(() => {
      wide = true
      for (const fn of listeners) fn()
    })
    expect(screen.queryByRole('dialog', { name: 'Menu' })).toBeNull()
    expect(listeners.size).toBe(0)
    await act(async () => {})
  })

  it('closes when the location changes by other means', async () => {
    stub([])
    const { router } = renderApp('/')
    fireEvent.click(opener())
    await act(async () => {
      await router.navigate('/server')
    })
    expect(screen.queryByRole('dialog', { name: 'Menu' })).toBeNull()
  })
})
