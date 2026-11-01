import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import Dialog from './Dialog'

describe('Dialog', () => {
  it('has an overlay that scrolls, so a tall dialog on a short window is never cut off', () => {
    render(
      <Dialog labelledBy="t" onClose={() => {}}>
        <h2 id="t">Tall</h2>
      </Dialog>,
    )
    const overlay = screen.getByRole('dialog').parentElement
    expect(overlay).toHaveClass('fixed', 'inset-0', 'overflow-y-auto', 'pt-[min(180px,15vh)]')
  })

  it('keeps the dim backdrop fixed, so scrolling a tall dialog never moves it', () => {
    render(
      <Dialog labelledBy="t" onClose={() => {}}>
        <h2 id="t">Tall</h2>
      </Dialog>,
    )
    expect(screen.getByTestId('confirm-overlay')).toHaveClass('fixed', 'inset-0')
    expect(screen.getByRole('dialog')).toHaveClass('relative')
  })

  it('keeps the centred-top look without sheet', () => {
    render(
      <Dialog labelledBy="t" onClose={() => {}}>
        <h2 id="t">Plain</h2>
      </Dialog>,
    )
    const overlay = screen.getByRole('dialog').parentElement
    expect(overlay).not.toHaveClass('max-sm:items-end')
    expect(screen.queryByTestId('sheet-handle')).toBeNull()
  })

  it('is a bottom sheet below 640 px with sheet: bottom-aligned, full width, rounded top, a grab handle', () => {
    render(
      <Dialog labelledBy="t" onClose={() => {}} sheet>
        <h2 id="t">Sheet</h2>
      </Dialog>,
    )
    const dialog = screen.getByRole('dialog')
    expect(dialog.parentElement).toHaveClass('max-sm:items-end', 'max-sm:p-0')
    expect(dialog).toHaveClass('max-sm:w-full', 'max-sm:rounded-b-none', 'max-sm:rounded-t-2xl')
    const handle = screen.getByTestId('sheet-handle')
    expect(handle).toHaveClass('hidden', 'max-sm:block', 'h-1', 'w-10')
    expect(handle).toHaveAttribute('aria-hidden', 'true')
  })
})
