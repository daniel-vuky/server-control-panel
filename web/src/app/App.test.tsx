import { render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { describe, expect, it } from 'vitest'
import { AppRoutes } from './App'

const renderAt = (path: string) =>
  render(
    <MemoryRouter initialEntries={[path]}>
      <AppRoutes />
    </MemoryRouter>,
  )

describe('app shell', () => {
  it('shows the six nav entries in design order', () => {
    renderAt('/')
    const nav = screen.getByRole('navigation', { name: 'Main navigation' })
    expect(
      within(nav)
        .getAllByRole('link')
        .map((a) => a.textContent?.trim()),
    ).toEqual(['Dashboard', 'Projects', 'Backups', 'Server', 'Activity', 'Settings'])
  })

  it.each([
    ['/', 'Dashboard'],
    ['/projects', 'Projects'],
    ['/projects/shop-api', 'Project detail'],
    ['/onboarding', 'Add a workspace'],
    ['/backups', 'Backups'],
    ['/server', 'Server'],
    ['/activity', 'Activity'],
    ['/settings', 'Settings'],
  ])('%s renders its page heading', (path, title) => {
    renderAt(path)
    expect(screen.getByRole('heading', { level: 1, name: title })).toBeInTheDocument()
  })

  it('maps / to the Dashboard, not its placeholder', async () => {
    renderAt('/')
    // The default fetch stub answers 401, so the Dashboard ends in its error state.
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not load projects.')
    expect(screen.queryByText(/Coming in/)).toBeNull()
  })

  it('maps /settings to the Settings page, not its placeholder', async () => {
    renderAt('/settings')
    expect(screen.getByRole('region', { name: 'Workspaces' })).toBeInTheDocument()
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not load workspaces.')
  })

  it('maps /onboarding to the onboarding page, not its placeholder', () => {
    renderAt('/onboarding')
    expect(screen.getByRole('region', { name: 'Add project folder' })).toBeInTheDocument()
    expect(screen.queryByText(/Coming in/)).toBeNull()
  })

  it('keeps the other routes as placeholders', () => {
    renderAt('/backups')
    expect(screen.getByText(/Coming in/)).toBeInTheDocument()
  })

  it('marks the current page in the nav', () => {
    renderAt('/backups')
    expect(screen.getByRole('link', { name: 'Backups' })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('link', { name: 'Dashboard' })).not.toHaveAttribute('aria-current')
  })

  it('renders Page not found for unknown paths', () => {
    renderAt('/nope')
    expect(screen.getByRole('heading', { level: 1, name: 'Page not found' })).toBeInTheDocument()
  })
})
