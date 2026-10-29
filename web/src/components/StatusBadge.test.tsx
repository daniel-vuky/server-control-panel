import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import StatusBadge, { type Status } from './StatusBadge'

describe('StatusBadge', () => {
  it.each<[Status, string]>([
    ['running', 'Running'],
    ['partial', 'Partial'],
    ['stopped', 'Stopped'],
    ['error', 'Error'],
    ['unknown', 'Unknown'],
  ])('%s renders the word %s with a dot', (status, word) => {
    const { container } = render(<StatusBadge status={status} />)
    expect(screen.getByText(word)).toBeInTheDocument()
    expect(container.querySelector('[aria-hidden="true"]')).not.toBeNull()
  })

  it('falls back to Unknown for a value it does not know', () => {
    render(<StatusBadge status="weird" />)
    expect(screen.getByText('Unknown')).toBeInTheDocument()
  })
})
