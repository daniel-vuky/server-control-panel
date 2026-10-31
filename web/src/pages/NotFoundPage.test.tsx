import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { describe, expect, it } from 'vitest'
import { LiveProvider } from '../app/live'
import NotFoundPage from './NotFoundPage'

describe('NotFoundPage', () => {
  it('says the page is not found and links back to the Dashboard', () => {
    render(
      <LiveProvider>
        <MemoryRouter>
          <NotFoundPage />
        </MemoryRouter>
      </LiveProvider>,
    )
    expect(screen.getByRole('heading', { level: 1, name: 'Page not found' })).toBeTruthy()
    expect(screen.getByRole('link', { name: 'Back to Dashboard' }).getAttribute('href')).toBe('/')
  })
})
