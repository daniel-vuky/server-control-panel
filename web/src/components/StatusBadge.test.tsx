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
    expect(spinner).toHaveClass('motion-safe:animate-[spin_0.8s_linear_infinite]')
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

  it('plain is the dot and the word in the status colour, with no pill and no ring', () => {
    const { container } = render(<StatusBadge status="running" variant="plain" />)
    const badge = container.firstElementChild as HTMLElement
    expect(screen.getByText('Running')).toBeInTheDocument()
    expect(badge).toHaveClass('text-success', 'text-[13px]')
    expect(badge.className).not.toMatch(/\bbg-/)
    expect(badge).not.toHaveClass('rounded-full')
    const dot = container.querySelector('[aria-hidden="true"]') as HTMLElement
    expect(dot).toHaveClass('size-[7px]', 'bg-success')
    expect(dot.className).not.toMatch(/shadow-/)
  })

  it('pill keeps its tint and ring', () => {
    const { container } = render(<StatusBadge status="running" />)
    const badge = container.firstElementChild as HTMLElement
    expect(badge).toHaveClass('rounded-full', 'bg-[rgba(63,207,142,0.12)]')
    expect(container.querySelector('[aria-hidden="true"]')?.className).toMatch(/shadow-/)
  })
})
