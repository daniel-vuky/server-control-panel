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
    ['/onboarding', 'Onboarding'],
    ['/backups', 'Backups'],
    ['/server', 'Server'],
    ['/activity', 'Activity'],
    ['/settings', 'Settings'],
  ])('%s renders its placeholder', (path, title) => {
    renderAt(path)
    expect(screen.getByRole('heading', { level: 1, name: title })).toBeInTheDocument()
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
