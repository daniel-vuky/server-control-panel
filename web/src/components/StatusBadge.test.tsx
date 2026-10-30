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
    ['notfound', 'Not found'],
  ])('%s renders the word %s with a dot', (status, word) => {
    const { container } = render(<StatusBadge status={status} />)
    expect(screen.getByText(word)).toBeInTheDocument()
    expect(container.querySelector('[aria-hidden="true"]')).not.toBeNull()
    expect(screen.queryByTestId('status-spinner')).toBeNull()
  })

  it.each<[Status, string]>([
    ['starting', 'Starting…'],
    ['stopping', 'Stopping…'],
    ['restarting', 'Restarting…'],
  ])('%s renders the word %s with a spinner', (status, word) => {
    render(<StatusBadge status={status} />)
    expect(screen.getByText(word)).toBeInTheDocument()
    const spinner = screen.getByTestId('status-spinner')
    expect(spinner).toHaveAttribute('aria-hidden', 'true')
    expect(spinner).toHaveClass('motion-safe:animate-spin')
  })

  it('strikes through Not found and no other word', () => {
    const { rerender } = render(<StatusBadge status="notfound" />)
    expect(screen.getByText('Not found')).toHaveClass('line-through')
    rerender(<StatusBadge status="running" />)
    expect(screen.getByText('Running')).not.toHaveClass('line-through')
  })

  it('falls back to Unknown for a value it does not know', () => {
    render(<StatusBadge status="weird" />)
    expect(screen.getByText('Unknown')).toBeInTheDocument()
  })
})
