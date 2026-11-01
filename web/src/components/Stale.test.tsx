import { fireEvent, render, screen } from '@testing-library/react'
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import Stale from './Stale'

/** Counter keeps a count in its own state, to see it survive the banner. */
function Counter() {
  const [n, setN] = useState(0)
  return (
    <button type="button" onClick={() => setN(n + 1)}>
      {`Count ${n}`}
    </button>
  )
}

describe('Stale', () => {
  it('shows the children as they are without an error', () => {
    render(
      <Stale error="" onRetry={() => {}} title="Could not refresh.">
        <p>old data</p>
      </Stale>,
    )
    expect(screen.queryByRole('alert')).toBeNull()
    const box = screen.getByText('old data').parentElement as HTMLElement
    expect(box.style.opacity).toBe('')
    expect(box.style.filter).toBe('')
    expect(box).not.toHaveAttribute('data-stale')
  })

  it('shows the error banner above the children and dims them to 55 %, saturation 0.6', () => {
    const onRetry = vi.fn()
    render(
      <Stale error="Network is down." onRetry={onRetry} title="Could not refresh the list.">
        <p>old data</p>
      </Stale>,
    )
    const alert = screen.getByRole('alert')
    expect(alert).toHaveTextContent('Could not refresh the list.')
    expect(alert).toHaveTextContent('Network is down.')
    const box = screen.getByText('old data').parentElement as HTMLElement
    expect(box).toHaveAttribute('data-stale')
    expect(box.style.opacity).toBe('0.55')
    expect(box.style.filter).toBe('saturate(0.6)')
    // The banner comes first, and is not dimmed.
    expect(alert.compareDocumentPosition(box) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(box.contains(alert)).toBe(false)
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(onRetry).toHaveBeenCalledTimes(1)
  })

  it('wraps the banner in bannerClassName and lays out the box with className', () => {
    render(
      <Stale
        error="x"
        onRetry={() => {}}
        title="t"
        className="grid gap-3"
        bannerClassName="px-4 py-3.5"
      >
        <p>data</p>
      </Stale>,
    )
    expect(screen.getByRole('alert').parentElement).toHaveClass('px-4', 'py-3.5')
    expect(screen.getByText('data').parentElement).toHaveClass('grid', 'gap-3')
  })

  it('keeps the children mounted when an error comes and goes', () => {
    const { rerender } = render(
      <Stale error="" onRetry={() => {}} title="t">
        <Counter />
      </Stale>,
    )
    fireEvent.click(screen.getByRole('button', { name: 'Count 0' }))
    rerender(
      <Stale error="down" onRetry={() => {}} title="t">
        <Counter />
      </Stale>,
    )
    expect(screen.getByRole('button', { name: 'Count 1' })).toBeInTheDocument()
    rerender(
      <Stale error="" onRetry={() => {}} title="t">
        <Counter />
      </Stale>,
    )
    expect(screen.getByRole('button', { name: 'Count 1' })).toBeInTheDocument()
  })
})
