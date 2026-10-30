import { fireEvent, render, screen } from '@testing-library/react'
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import ConfirmDialog from './ConfirmDialog'

function setup(tone: 'danger' | 'primary' = 'danger') {
  const onConfirm = vi.fn()
  const onCancel = vi.fn()
  render(
    <ConfirmDialog
      title={<>Stop shop?</>}
      body="It will stop serving."
      command="docker compose stop"
      confirmLabel="Stop"
      tone={tone}
      onConfirm={onConfirm}
      onCancel={onCancel}
    />,
  )
  return { onConfirm, onCancel }
}

function Host() {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        opener
      </button>
      {open && (
        <ConfirmDialog
          title="Stop shop?"
          body="x"
          command="docker compose stop"
          confirmLabel="Stop"
          tone="danger"
          onConfirm={() => setOpen(false)}
          onCancel={() => setOpen(false)}
        />
      )}
    </>
  )
}

describe('ConfirmDialog', () => {
  it('is a modal dialog labelled by its title', () => {
    setup()
    const dialog = screen.getByRole('dialog', { name: 'Stop shop?' })
    expect(dialog).toHaveAttribute('aria-modal', 'true')
    expect(screen.getByText('It will stop serving.')).toBeInTheDocument()
  })

  it('shows the command box with the timeout', () => {
    setup()
    expect(screen.getByText('$ docker compose stop · timeout 60s')).toBeInTheDocument()
  })

  it('shows no command box when there is no command', () => {
    render(
      <ConfirmDialog
        title="Remove it?"
        body="Nothing on the server is touched."
        confirmLabel="Remove"
        tone="danger"
        onConfirm={() => {}}
        onCancel={() => {}}
      />,
    )
    expect(screen.getByText('Nothing on the server is touched.')).toBeInTheDocument()
    expect(screen.queryByText(/timeout/)).toBeNull()
    expect(screen.queryByText(/\$ /)).toBeNull()
  })

  it('starts with focus on Cancel', () => {
    setup()
    expect(screen.getByRole('button', { name: 'Cancel' })).toHaveFocus()
  })

  it('Escape calls only onCancel', () => {
    const { onCancel, onConfirm } = setup()
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' })
    expect(onCancel).toHaveBeenCalledTimes(1)
    expect(onConfirm).not.toHaveBeenCalled()
  })

  it('a click on the overlay calls only onCancel', () => {
    const { onCancel, onConfirm } = setup()
    fireEvent.click(screen.getByTestId('confirm-overlay'))
    expect(onCancel).toHaveBeenCalledTimes(1)
    expect(onConfirm).not.toHaveBeenCalled()
  })

  it('a click inside the dialog does not cancel', () => {
    const { onCancel } = setup()
    fireEvent.click(screen.getByRole('dialog'))
    expect(onCancel).not.toHaveBeenCalled()
  })

  it('Cancel calls only onCancel', () => {
    const { onCancel, onConfirm } = setup()
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(onCancel).toHaveBeenCalledTimes(1)
    expect(onConfirm).not.toHaveBeenCalled()
  })

  it('confirm calls only onConfirm', () => {
    const { onCancel, onConfirm } = setup()
    fireEvent.click(screen.getByRole('button', { name: 'Stop' }))
    expect(onConfirm).toHaveBeenCalledTimes(1)
    expect(onCancel).not.toHaveBeenCalled()
  })

  it('danger uses the red class', () => {
    setup('danger')
    expect(screen.getByRole('button', { name: 'Stop' })).toHaveClass('bg-stop')
  })

  it('primary uses the light button', () => {
    setup('primary')
    const confirm = screen.getByRole('button', { name: 'Stop' })
    expect(confirm).toHaveClass('bg-text')
    expect(confirm).not.toHaveClass('bg-stop')
  })

  it('Tab cycles inside the dialog', () => {
    setup()
    const cancel = screen.getByRole('button', { name: 'Cancel' })
    const confirm = screen.getByRole('button', { name: 'Stop' })
    confirm.focus()
    fireEvent.keyDown(confirm, { key: 'Tab' })
    expect(cancel).toHaveFocus()
    fireEvent.keyDown(cancel, { key: 'Tab', shiftKey: true })
    expect(confirm).toHaveFocus()
  })

  it('returns focus to the opener when it closes', () => {
    render(<Host />)
    const opener = screen.getByRole('button', { name: 'opener' })
    opener.focus()
    fireEvent.click(opener)
    expect(screen.getByRole('button', { name: 'Cancel' })).toHaveFocus()
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' })
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(opener).toHaveFocus()
  })
})
