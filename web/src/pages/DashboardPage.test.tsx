import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Project } from '../api/types.gen'
import DashboardPage from './DashboardPage'

const projects: Project[] = [
  {
    id: 1,
    displayName: 'shop',
    path: '/srv/work/shop',
    workspaceId: 1,
    self: false,
    status: 'running',
    databases: [
      { type: 'postgresql', name: 'shop', locationKind: 'container', location: 'db', sources: [] },
      { type: 'sqlite', name: 'cache', locationKind: 'file', location: './cache.db', sources: [] },
    ],
  },
  {
    id: 2,
    displayName: 'panel',
    path: '/srv/work/panel',
    workspaceId: 1,
    self: true,
    status: 'partial',
    databases: [],
  },
]

/** serve stubs fetch with one answer per call, the last one repeating. */
function serve(...answers: Array<[number, unknown]>) {
  let n = 0
  const fetchStub = vi.fn(async () => {
    const [status, body] = answers[Math.min(n++, answers.length - 1)]
    return new Response(JSON.stringify(body), { status })
  })
  vi.stubGlobal('fetch', fetchStub)
  return fetchStub
}

const renderPage = () =>
  render(
    <MemoryRouter>
      <DashboardPage />
    </MemoryRouter>,
  )

afterEach(() => vi.unstubAllGlobals())

describe('DashboardPage', () => {
  it('shows the heading and a loading state before the answer', () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => new Promise(() => {})),
    )
    renderPage()
    expect(screen.getByRole('heading', { level: 1, name: 'Dashboard' })).toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent('Loading projects')
  })

  it('lists each tracked project with name, path, status and databases', async () => {
    const fetchStub = serve([200, projects])
    renderPage()

    const shop = await screen.findByRole('row', { name: /shop/ })
    expect(fetchStub.mock.calls[0]).toEqual(['/api/projects', expect.anything()])
    expect(within(shop).getByText('shop')).toBeInTheDocument()
    expect(within(shop).getByText('/srv/work/shop')).toBeInTheDocument()
    expect(within(shop).getByText('Running')).toBeInTheDocument()
    expect(within(shop).getByText('PostgreSQL · container db')).toBeInTheDocument()
    expect(within(shop).getByText('SQLite · file')).toBeInTheDocument()
    expect(within(shop).queryByText('Monitor only')).toBeNull()

    const panel = screen.getByRole('row', { name: /panel/ })
    expect(within(panel).getByText('Monitor only')).toBeInTheDocument()
    expect(within(panel).getByText('/srv/work/panel')).toBeInTheDocument()
    expect(within(panel).getByText('Partial')).toBeInTheDocument()
    expect(within(panel).getByText('None detected')).toBeInTheDocument()

    expect(screen.getAllByRole('columnheader').map((h) => h.textContent?.trim())).toEqual([
      'Display name',
      'Path',
      'Status',
      'Database',
    ])
  })

  it('shows the empty state with an Add workspace link to onboarding', async () => {
    serve([200, []])
    renderPage()
    expect(await screen.findByText('No projects yet')).toBeInTheDocument()
    expect(screen.queryByRole('table')).toBeNull()
    expect(screen.getByRole('link', { name: 'Add workspace' })).toHaveAttribute(
      'href',
      '/onboarding',
    )
  })

  it('shows an error with a retry that loads again', async () => {
    const fetchStub = serve([500, { error: 'docker is down' }], [200, projects])
    renderPage()

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Could not load projects')
    expect(alert).toHaveTextContent('docker is down')

    fireEvent.click(within(alert).getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('/srv/work/shop')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).toBeNull()
    expect(fetchStub).toHaveBeenCalledTimes(2)
  })

  it('treats a non-JSON 2xx as an error', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response('<html>login</html>', { status: 200 })),
    )
    renderPage()
    await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument())
    expect(screen.getByRole('alert')).toHaveTextContent('Unexpected response from the server.')
  })

  it('treats a JSON answer that is not a list as an error', async () => {
    serve([200, { email: 'someone@example.com' }])
    renderPage()
    expect(await screen.findByRole('alert')).toHaveTextContent('Unexpected response')
  })
})
