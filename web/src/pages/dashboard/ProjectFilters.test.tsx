import { fireEvent, render, screen, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { Filters } from './filters'
import ProjectFilters from './ProjectFilters'

const counts = { all: 6, running: 3, partial: 1, stopped: 1, error: 1 }
const filters: Filters = { status: 'all', q: '', view: 'cards' }

function setup(f: Partial<Filters> = {}) {
  const onChange = vi.fn()
  render(<ProjectFilters filters={{ ...filters, ...f }} counts={counts} onChange={onChange} />)
  return onChange
}

describe('ProjectFilters', () => {
  it('shows a toggle chip per status with its count, the chosen one pressed', () => {
    setup({ status: 'partial' })
    const group = screen.getByRole('group', { name: 'Filter by status' })
    const chips = within(group).getAllByRole('button')
    expect(chips.map((c) => c.textContent)).toEqual([
      'All6',
      'Running3',
      'Partial1',
      'Stopped1',
      'Error1',
    ])
    expect(chips.map((c) => c.getAttribute('aria-pressed'))).toEqual([
      'false',
      'false',
      'true',
      'false',
      'false',
    ])
  })

  it('picks a status', () => {
    const onChange = setup()
    fireEvent.click(screen.getByRole('button', { name: /^Error/ }))
    expect(onChange).toHaveBeenCalledWith({ status: 'error' })
  })

  it('types into the search box, labelled for screen readers', () => {
    const onChange = setup({ q: 'sh' })
    const input = screen.getByLabelText('Search projects by name')
    expect(input).toHaveAttribute('placeholder', 'Search by name…')
    expect(input).toHaveValue('sh')
    fireEvent.change(input, { target: { value: 'shop' } })
    expect(onChange).toHaveBeenCalledWith({ q: 'shop' })
  })

  it('switches the view with a pressed group', () => {
    const onChange = setup({ view: 'table' })
    const group = screen.getByRole('group', { name: 'View mode' })
    expect(within(group).getByRole('button', { name: 'Cards' })).toHaveAttribute(
      'aria-pressed',
      'false',
    )
    expect(within(group).getByRole('button', { name: 'Table' })).toHaveAttribute(
      'aria-pressed',
      'true',
    )
    fireEvent.click(within(group).getByRole('button', { name: 'Cards' }))
    expect(onChange).toHaveBeenCalledWith({ view: 'cards' })
  })
})
