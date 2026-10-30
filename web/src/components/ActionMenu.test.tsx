import { fireEvent, render, screen } from '@testing-library/react'
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import ActionMenu from './ActionMenu'
import ConfirmDialog from './ConfirmDialog'

function MenuWithDialog() {
  const [confirm, setConfirm] = useState(false)
  return (
    <>
      <ActionMenu
        name="shop"
        actions={['restart']}
        onStart={() => {}}
        onRestart={() => setConfirm(true)}
      />
      {confirm && (
        <ConfirmDialog
          title="Restart shop?"
          body="x"
          command="docker compose restart"
          confirmLabel="Restart"
          tone="primary"
          onConfirm={() => setConfirm(false)}
          onCancel={() => setConfirm(false)}
        />
      )}
    </>
  )
}

function setup(actions: string[]) {
  const onStart = vi.fn()
  const onRestart = vi.fn()
  render(
    <div>
      <ActionMenu name="shop" actions={actions} onStart={onStart} onRestart={onRestart} />
      <button type="button">outside</button>
    </div>,
  )
  const trigger = screen.getByRole('button', { name: 'More actions for shop' })
  return { onStart, onRestart, trigger }
}

const open = (trigger: HTMLElement) => fireEvent.click(trigger)

describe('ActionMenu', () => {
  it('has a closed menu button with the right ARIA', () => {
    const { trigger } = setup(['start', 'restart'])
    expect(trigger).toHaveAttribute('aria-haspopup', 'menu')
    expect(trigger).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByRole('menu')).toBeNull()
  })

  it('opens a menu with Start and Restart', () => {
    const { trigger } = setup(['start', 'restart'])
    open(trigger)
    expect(trigger).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByRole('menu', { name: 'Actions for shop' })).toBeInTheDocument()
    expect(screen.getByRole('menuitem', { name: 'Start' })).toBeInTheDocument()
    expect(screen.getByRole('menuitem', { name: 'Restart' })).toBeInTheDocument()
  })

  it('disables items that are not in actions and ignores clicks on them', () => {
    const { trigger, onStart, onRestart } = setup(['restart'])
    open(trigger)
    const start = screen.getByRole('menuitem', { name: 'Start' })
    expect(start).toHaveAttribute('aria-disabled', 'true')
    fireEvent.click(start)
    expect(onStart).not.toHaveBeenCalled()
    expect(screen.getByRole('menu')).toBeInTheDocument()
    const restart = screen.getByRole('menuitem', { name: 'Restart' })
    expect(restart).not.toHaveAttribute('aria-disabled', 'true')
    fireEvent.click(restart)
    expect(onRestart).toHaveBeenCalledTimes(1)
    expect(screen.queryByRole('menu')).toBeNull()
  })

  it('an enabled item runs its handler and closes the menu', () => {
    const { trigger, onStart } = setup(['start'])
    open(trigger)
    fireEvent.click(screen.getByRole('menuitem', { name: 'Start' }))
    expect(onStart).toHaveBeenCalledTimes(1)
    expect(screen.queryByRole('menu')).toBeNull()
  })

  it('Escape closes and returns focus to the button', () => {
    const { trigger } = setup(['start', 'restart'])
    open(trigger)
    fireEvent.keyDown(screen.getByRole('menu'), { key: 'Escape' })
    expect(screen.queryByRole('menu')).toBeNull()
    expect(trigger).toHaveFocus()
  })

  it('an outside click closes and returns focus to the button', () => {
    const { trigger } = setup(['start', 'restart'])
    open(trigger)
    fireEvent.mouseDown(screen.getByRole('button', { name: 'outside' }))
    expect(screen.queryByRole('menu')).toBeNull()
    expect(trigger).toHaveFocus()
  })

  it('focuses the first item on open and the arrows move focus', () => {
    const { trigger } = setup(['start', 'restart'])
    open(trigger)
    const start = screen.getByRole('menuitem', { name: 'Start' })
    const restart = screen.getByRole('menuitem', { name: 'Restart' })
    expect(start).toHaveFocus()
    fireEvent.keyDown(screen.getByRole('menu'), { key: 'ArrowDown' })
    expect(restart).toHaveFocus()
    fireEvent.keyDown(screen.getByRole('menu'), { key: 'ArrowDown' })
    expect(start).toHaveFocus()
    fireEvent.keyDown(screen.getByRole('menu'), { key: 'ArrowUp' })
    expect(restart).toHaveFocus()
  })

  it('disabled items stay focusable so the arrows still reach them', () => {
    const { trigger } = setup([])
    open(trigger)
    const start = screen.getByRole('menuitem', { name: 'Start' })
    expect(start).toHaveFocus()
  })

  it('the button toggles the menu', () => {
    const { trigger } = setup(['start'])
    open(trigger)
    open(trigger)
    expect(screen.queryByRole('menu')).toBeNull()
  })

  it('activating an item returns focus to the button', () => {
    const { trigger } = setup(['start', 'restart'])
    open(trigger)
    fireEvent.click(screen.getByRole('menuitem', { name: 'Restart' }))
    expect(trigger).toHaveFocus()
  })

  it('a dialog opened from an item hands focus back to the button after Escape', () => {
    render(<MenuWithDialog />)
    const trigger = screen.getByRole('button', { name: 'More actions for shop' })
    fireEvent.click(trigger)
    fireEvent.click(screen.getByRole('menuitem', { name: 'Restart' }))
    expect(screen.getByRole('button', { name: 'Cancel' })).toHaveFocus()
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' })
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(trigger).toHaveFocus()
  })
})
