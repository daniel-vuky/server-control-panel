import { render, screen } from '@testing-library/react'
import { Search } from 'lucide-react'
import { MemoryRouter } from 'react-router'
import { describe, expect, it } from 'vitest'
import EmptyState, { EmptyStateButton, EmptyStateLink } from './EmptyState'

describe('EmptyState', () => {
  it('renders the title as a level 2 heading, the line and the action', () => {
    const { container } = render(
      <EmptyState
        icon={Search}
        title="Scan found no projects"
        action={<EmptyStateButton>Scan again</EmptyStateButton>}
      >
        Try a deeper scan.
      </EmptyState>,
    )
    expect(screen.getByRole('heading', { level: 2, name: 'Scan found no projects' })).toBeTruthy()
    expect(screen.getByText('Try a deeper scan.')).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Scan again' })).toBeTruthy()
    expect(container.querySelector('svg')?.getAttribute('aria-hidden')).toBe('true')
  })

  it('renders the title as a level 1 heading when asked', () => {
    render(<EmptyState icon={Search} title="Page not found" titleAs="h1" />)
    expect(screen.getByRole('heading', { level: 1, name: 'Page not found' })).toBeTruthy()
  })

  it('works without a line or an action', () => {
    render(<EmptyState icon={Search} title="Nothing here" />)
    expect(screen.getByRole('heading', { level: 2, name: 'Nothing here' })).toBeTruthy()
    expect(screen.queryByRole('button')).toBeNull()
  })

  it('offers a link variant of the action', () => {
    render(
      <MemoryRouter>
        <EmptyState
          icon={Search}
          title="Gone"
          action={<EmptyStateLink to="/">Back</EmptyStateLink>}
        />
      </MemoryRouter>,
    )
    expect(screen.getByRole('link', { name: 'Back' }).getAttribute('href')).toBe('/')
  })
})
