import { fireEvent, render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Workspace } from '../api/types.gen'
import { formatLastScan } from '../lib/time'
import SettingsPage from './SettingsPage'

const scanned = new Date(2026, 0, 5, 9, 42)

const workspaces: Workspace[] = [
  {
    id: 1,
    name: 'projects',
    path: '/home/deploy/projects',
    depth: 1,
    lastScanAt: scanned.toISOString(),
    tracked: 2,
    ignored: 1,
  },
  {
    id: 2,
    name: 'solo',
    path: '/srv/solo',
    depth: 3,
    lastScanAt: scanned.toISOString(),
    tracked: 1,
    ignored: 0,
  },
]

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
      <SettingsPage />
    </MemoryRouter>,
  )

afterEach(() => vi.unstubAllGlobals())

describe('SettingsPage', () => {
  it('shows the heading, and loading before the answer', () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => new Promise(() => {})),
    )
    renderPage()
    expect(screen.getByRole('heading', { level: 1, name: 'Settings' })).toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent('Loading workspaces')
  })

  it('lists each workspace with its path and summary line', async () => {
    const fetchStub = serve([200, workspaces])
    renderPage()

    const card = screen.getByRole('region', { name: 'Workspaces' })
    expect(await within(card).findByText('/home/deploy/projects')).toBeInTheDocument()
    expect(fetchStub.mock.calls[0]).toEqual(['/api/workspaces', expect.anything()])
    expect(
      within(card).getByText(
        `Depth 1 · 2 projects tracked · 1 ignored · last scanned ${formatLastScan(scanned.toISOString())}`,
      ),
    ).toBeInTheDocument()
    expect(within(card).getByText('/srv/solo')).toBeInTheDocument()
    expect(
      within(card).getByText(
        `Depth 3 · 1 project tracked · 0 ignored · last scanned ${formatLastScan(scanned.toISOString())}`,
      ),
    ).toBeInTheDocument()
    expect(within(card).getByRole('link', { name: 'Add workspace' })).toHaveAttribute(
      'href',
      '/onboarding',
    )
    expect(within(card).queryByRole('button', { name: 'Rescan' })).toBeNull()
    expect(within(card).queryByRole('button', { name: 'Remove' })).toBeNull()
  })

  it('shows the empty state, still with Add workspace', async () => {
    serve([200, []])
    renderPage()
    const card = screen.getByRole('region', { name: 'Workspaces' })
    expect(await within(card).findByText('No workspace yet')).toBeInTheDocument()
    expect(within(card).getByRole('link', { name: 'Add workspace' })).toHaveAttribute(
      'href',
      '/onboarding',
    )
  })

  it('shows an error in the card with a retry', async () => {
    const fetchStub = serve([500, { error: 'boom' }], [200, workspaces])
    renderPage()
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Could not load workspaces')
    fireEvent.click(within(alert).getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('/srv/solo')).toBeInTheDocument()
    expect(fetchStub).toHaveBeenCalledTimes(2)
  })

  it('treats a JSON answer that is not a list as an error', async () => {
    serve([200, {}])
    renderPage()
    expect(await screen.findByRole('alert')).toHaveTextContent('Unexpected response')
  })

  it('points the other sections at the issues that will build them', async () => {
    serve([200, []])
    renderPage()
    const issueUrl = (n: number) =>
      `https://github.com/daniel-vuky/server-control-panel/issues/${n}`
    for (const [name, issue] of [
      ['Google Drive', 3],
      ['Alerts', 8],
      ['Backup defaults', 6],
    ] as const) {
      const section = screen.getByRole('region', { name })
      expect(section).toHaveTextContent('Coming in #')
      expect(within(section).getByRole('link', { name: `#${issue}` })).toHaveAttribute(
        'href',
        issueUrl(issue),
      )
    }
    await screen.findByText('No workspace yet')
  })
})
