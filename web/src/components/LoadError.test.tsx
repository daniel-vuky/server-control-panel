import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import LoadError from './LoadError'

describe('LoadError', () => {
  it("is design 10's error banner with the message and a Retry button", () => {
    const onRetry = vi.fn()
    render(
      <LoadError title="Could not load backups." message="Network is down." onRetry={onRetry} />,
    )
    const alert = screen.getByRole('alert')
    expect(alert).toHaveClass('border-error-border', 'bg-error-bg', 'text-error-fg')
    expect(alert).toHaveTextContent('Could not load backups.')
    expect(screen.getByText('Network is down.')).toHaveClass('text-error-text')
    expect(alert.querySelector('svg')).toHaveClass('text-danger')
    const retry = screen.getByRole('button', { name: 'Retry' })
    expect(retry).toHaveClass('h-8', 'rounded-md', 'border-error-strong', 'text-error-fg')
    fireEvent.click(retry)
    expect(onRetry).toHaveBeenCalledTimes(1)
  })

  it('disables Retry and reads Retrying… while busy', () => {
    const onRetry = vi.fn()
    const { rerender } = render(
      <LoadError title="Could not load backups." message="Down." onRetry={onRetry} busy />,
    )
    const retry = screen.getByRole('button', { name: 'Retrying…' })
    expect(retry).toBeDisabled()
    expect(retry).toHaveAttribute('aria-busy', 'true')
    fireEvent.click(retry)
    expect(onRetry).not.toHaveBeenCalled()
    rerender(<LoadError title="Could not load backups." message="Down." onRetry={onRetry} />)
    expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled()
  })
})
