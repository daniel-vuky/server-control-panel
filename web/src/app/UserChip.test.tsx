import { act, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { describe, expect, it, vi } from 'vitest'
import { AppRoutes } from './App'
import { initials } from './UserChip'

const renderApp = () =>
  render(
    <MemoryRouter initialEntries={['/']}>
      <AppRoutes />
    </MemoryRouter>,
  )

const stubFetch = (impl: () => Promise<Response>) => {
  vi.stubGlobal('fetch', vi.fn(impl))
}

const expectNoChip = async () => {
  await waitFor(() => expect(fetch).toHaveBeenCalled())
  await act(async () => {
    await new Promise((r) => setTimeout(r, 0))
  })
  expect(screen.queryByText('Via Cloudflare Access')).toBeNull()
}

describe('user chip', () => {
  it('shows the signed-in email and initials', async () => {
    stubFetch(async () => new Response('{"email":"admin@company.vn"}', { status: 200 }))
    renderApp()
    expect(await screen.findByText('admin@company.vn')).toBeInTheDocument()
    expect(screen.getByText('AD')).toBeInTheDocument()
    expect(screen.getByText('Via Cloudflare Access')).toBeInTheDocument()
    const nav = screen.getByRole('navigation', { name: 'Main navigation' })
    expect(within(nav).getByText('admin@company.vn')).toBeInTheDocument()
  })

  it('shows no chip when /api/me fails', async () => {
    renderApp()
    await expectNoChip()
  })

  it('shows no chip for a non-JSON 200', async () => {
    stubFetch(async () => new Response('<html>login</html>', { status: 200 }))
    renderApp()
    await expectNoChip()
  })

  it('shows no chip when email is missing', async () => {
    stubFetch(async () => new Response('{}', { status: 200 }))
    renderApp()
    await expectNoChip()
  })

  it('shows no chip when fetch rejects', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('network')))
    renderApp()
    await expectNoChip()
  })
})

describe('initials', () => {
  it.each([
    ['admin@company.vn', 'AD'],
    ['x@y', 'X'],
    ['@y', '?'],
  ])('%s -> %s', (email, want) => {
    expect(initials(email)).toBe(want)
  })
})
