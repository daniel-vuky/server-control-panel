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
})
