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
    expect(container.querySelector('[data-testid="status-dot"]')).not.toBeNull()
  })

  it('falls back to Unknown for a value it does not know', () => {
    render(<StatusBadge status={'weird' as Status} />)
    expect(screen.getByText('Unknown')).toBeInTheDocument()
  })
})
