import { screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { renderApp } from '../test/renderApp'

const renderAt = (path: string) => renderApp(path)

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
    const workspaces = screen.getByRole('region', { name: 'Workspaces' })
    expect(await within(workspaces).findByRole('alert')).toHaveTextContent(
      'Could not load workspaces.',
    )
    expect(screen.getByRole('region', { name: 'Backup defaults' })).toBeInTheDocument()
  })

  it('shows the Google return toast on any page of the shell', async () => {
    const { router } = renderAt('/backups?drive=declined')
    expect(await screen.findByRole('alert')).toHaveTextContent('Google Drive was not connected.')
    await waitFor(() => expect(router.state.location.search).toBe(''))
  })

  it('maps /onboarding to the onboarding page, not its placeholder', () => {
    renderAt('/onboarding')
    expect(screen.getByRole('region', { name: 'Add project folder' })).toBeInTheDocument()
    expect(screen.queryByText(/Coming in/)).toBeNull()
  })

  it('maps /projects/:id to the project page, not its placeholder', () => {
    renderAt('/projects/1')
    // No live state arrives here (no EventSource), so the page waits for it.
    expect(screen.getByText('Loading project…')).toBeInTheDocument()
    expect(screen.queryByText(/Coming in/)).toBeNull()
  })

  it('maps /backups to the Backups page, not its placeholder', () => {
    renderAt('/backups')
    // No live state arrives here (no EventSource), so the page waits for it.
    expect(screen.getByRole('status', { name: 'Loading' })).toBeInTheDocument()
    expect(screen.queryByText(/Coming in/)).toBeNull()
  })

  it('maps /server to the Server page, not its placeholder', async () => {
    renderAt('/server')
    // The default fetch stub answers 401, so the page ends in its error state.
    expect(await screen.findByText('Could not load the server.')).toBeInTheDocument()
    expect(screen.queryByText(/Coming in/)).toBeNull()
  })

  it('keeps the other routes as placeholders', () => {
    renderAt('/activity')
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
