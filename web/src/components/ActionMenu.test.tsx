import { act, fireEvent, render, screen } from '@testing-library/react'
import { useState } from 'react'
import { MemoryRouter, useLocation } from 'react-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
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

function Where() {
  return <output data-testid="where">{useLocation().pathname}</output>
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

  it('gives a disabled item its reason as a title when it knows the status', () => {
    render(
      <ActionMenu
        name="shop"
        actions={['stop']}
        status="running"
        onStart={() => {}}
        onRestart={() => {}}
      />,
    )
    open(screen.getByRole('button', { name: 'More actions for shop' }))
    expect(screen.getByRole('menuitem', { name: 'Start' })).toHaveAttribute(
      'title',
      'Not allowed while Running',
    )
  })

  describe('stop tracking and remove', () => {
    function withUntrack(actions: string[]) {
      const onUntrack = vi.fn()
      render(
        <ActionMenu
          name="shop"
          actions={actions}
          onStart={() => {}}
          onRestart={() => {}}
          onUntrack={onUntrack}
        />,
      )
      open(screen.getByRole('button', { name: 'More actions for shop' }))
      return onUntrack
    }

    it('shows Stop tracking, red, after a divider, when untrack is allowed', () => {
      const onUntrack = withUntrack(['stop', 'restart', 'untrack'])
      const item = screen.getByRole('menuitem', { name: 'Stop tracking' })
      expect(item).toHaveClass('text-danger')
      expect(item).not.toHaveAttribute('aria-disabled')
      expect(screen.queryByRole('menuitem', { name: 'Remove from list' })).toBeNull()
      const sep = screen.getByRole('separator')
      expect(sep.compareDocumentPosition(item) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
      fireEvent.click(item)
      expect(onUntrack).toHaveBeenCalledTimes(1)
      expect(screen.queryByRole('menu')).toBeNull()
    })

    it('shows Remove from list, red, when remove is allowed', () => {
      const onUntrack = withUntrack(['remove'])
      const item = screen.getByRole('menuitem', { name: 'Remove from list' })
      expect(item).toHaveClass('text-danger')
      expect(screen.queryByRole('menuitem', { name: 'Stop tracking' })).toBeNull()
      // Start and Restart stay, disabled.
      expect(screen.getByRole('menuitem', { name: 'Start' })).toHaveAttribute(
        'aria-disabled',
        'true',
      )
      fireEvent.click(item)
      expect(onUntrack).toHaveBeenCalledTimes(1)
    })

    it('shows neither item, and no divider, when neither is allowed', () => {
      withUntrack(['start', 'restart'])
      expect(screen.queryByRole('menuitem', { name: 'Stop tracking' })).toBeNull()
      expect(screen.queryByRole('menuitem', { name: 'Remove from list' })).toBeNull()
      expect(screen.queryByRole('separator')).toBeNull()
    })

    it('reaches the red item with the arrows', () => {
      withUntrack(['untrack'])
      fireEvent.keyDown(screen.getByRole('menu'), { key: 'ArrowUp' })
      expect(screen.getByRole('menuitem', { name: 'Stop tracking' })).toHaveFocus()
    })
  })

  it('Tab closes the menu and leaves focus on the trigger, so the page order continues from it', () => {
    const { trigger } = setup(['start', 'restart'])
    open(trigger)
    fireEvent.keyDown(screen.getByRole('menu'), { key: 'Tab' })
    expect(screen.queryByRole('menu')).toBeNull()
    expect(trigger).toHaveFocus()
    open(trigger)
    fireEvent.keyDown(screen.getByRole('menu'), { key: 'Tab', shiftKey: true })
    expect(screen.queryByRole('menu')).toBeNull()
    expect(trigger).toHaveFocus()
  })

  it('gives every item a 15px aria-hidden icon in a flex row', () => {
    render(
      <MemoryRouter>
        <ActionMenu
          name="shop"
          actions={['start', 'restart', 'untrack']}
          onStart={() => {}}
          onRestart={() => {}}
          onUntrack={() => {}}
          backup={{ enabled: true, reason: '', onRun: () => {} }}
          logsHref="/projects/7/logs"
          detailsHref="/projects/7"
        />
      </MemoryRouter>,
    )
    open(screen.getByRole('button', { name: 'More actions for shop' }))
    const items = screen.getAllByRole('menuitem')
    expect(items).toHaveLength(6)
    for (const item of items) {
      expect(item).toHaveClass('flex', 'items-center', 'gap-2.5')
      const icon = item.querySelector('svg')
      expect(icon).not.toBeNull()
      expect(icon).toHaveAttribute('aria-hidden', 'true')
      expect(icon).toHaveAttribute('width', '15')
      expect(icon).toHaveAttribute('height', '15')
      expect(icon).toHaveAttribute('stroke-width', '1.9')
    }
  })

  describe('View logs and Open details', () => {
    function withLinks(props: { logsDisabledReason?: string; links?: boolean } = {}) {
      const { links = true, logsDisabledReason } = props
      render(
        <MemoryRouter initialEntries={['/']}>
          <ActionMenu
            name="shop"
            actions={['start', 'restart', 'untrack']}
            onStart={() => {}}
            onRestart={() => {}}
            onUntrack={() => {}}
            backup={{ enabled: true, reason: '', onRun: () => {} }}
            logsHref={links ? '/projects/7/logs' : undefined}
            logsDisabledReason={logsDisabledReason}
            detailsHref={links ? '/projects/7' : undefined}
          />
          <Where />
        </MemoryRouter>,
      )
      open(screen.getByRole('button', { name: 'More actions for shop' }))
    }

    it('lists Start, Restart, Back up now, View logs, Open details, Stop tracking in order', () => {
      withLinks()
      expect(screen.getAllByRole('menuitem').map((el) => el.textContent)).toEqual([
        'Start',
        'Restart',
        'Back up now',
        'View logs',
        'Open details',
        'Stop tracking',
      ])
      expect(screen.getByRole('menuitem', { name: 'View logs' })).toHaveAttribute(
        'href',
        '/projects/7/logs',
      )
      expect(screen.getByRole('menuitem', { name: 'Open details' })).toHaveAttribute(
        'href',
        '/projects/7',
      )
    })

    it('arrow keys move through the link items too', () => {
      withLinks()
      const menu = screen.getByRole('menu')
      for (let i = 0; i < 3; i++) fireEvent.keyDown(menu, { key: 'ArrowDown' })
      expect(screen.getByRole('menuitem', { name: 'View logs' })).toHaveFocus()
      fireEvent.keyDown(menu, { key: 'ArrowDown' })
      expect(screen.getByRole('menuitem', { name: 'Open details' })).toHaveFocus()
      fireEvent.keyDown(menu, { key: 'ArrowDown' })
      expect(screen.getByRole('menuitem', { name: 'Stop tracking' })).toHaveFocus()
    })

    it('clicking a link navigates and closes the menu', () => {
      withLinks()
      fireEvent.click(screen.getByRole('menuitem', { name: 'Open details' }))
      expect(screen.getByTestId('where')).toHaveTextContent('/projects/7')
      expect(screen.queryByRole('menu')).toBeNull()
    })

    it('a disabled View logs is aria-disabled with the reason, focusable, and goes nowhere', () => {
      withLinks({ logsDisabledReason: 'The project folder is missing' })
      const logs = screen.getByRole('menuitem', { name: 'View logs' })
      expect(logs).toHaveAttribute('aria-disabled', 'true')
      expect(logs).toHaveAttribute('title', 'The project folder is missing')
      expect(logs).not.toHaveAttribute('href')
      fireEvent.click(logs)
      expect(screen.getByTestId('where').textContent).toBe('/')
      expect(screen.getByRole('menu')).toBeInTheDocument()
      for (let i = 0; i < 3; i++) fireEvent.keyDown(screen.getByRole('menu'), { key: 'ArrowDown' })
      expect(logs).toHaveFocus()
    })

    it('without the props the items are as before', () => {
      withLinks({ links: false })
      expect(screen.getAllByRole('menuitem').map((el) => el.textContent)).toEqual([
        'Start',
        'Restart',
        'Back up now',
        'Stop tracking',
      ])
    })
  })

  describe('placement', () => {
    afterEach(() => vi.restoreAllMocks())

    function openAt(rect: Partial<DOMRect>) {
      const { trigger } = setup(['start', 'restart'])
      vi.spyOn(trigger, 'getBoundingClientRect').mockReturnValue({
        top: 0,
        left: 0,
        width: 0,
        height: 0,
        x: 0,
        y: 0,
        toJSON: () => ({}),
        ...rect,
      } as DOMRect)
      open(trigger)
      return screen.getByRole('menu')
    }

    it('is fixed under the trigger, so a table overflow cannot clip it', () => {
      vi.stubGlobal('innerWidth', 1000)
      const menu = openAt({ bottom: 120, right: 940 })
      expect(menu.style.position).toBe('fixed')
      expect(menu.style.top).toBe('124px')
      expect(menu.style.right).toBe('60px')
      vi.unstubAllGlobals()
    })

    it('closes on a window scroll', () => {
      const menu = openAt({ bottom: 120, right: 940 })
      expect(menu).toBeInTheDocument()
      act(() => {
        window.dispatchEvent(new Event('scroll'))
      })
      expect(screen.queryByRole('menu')).toBeNull()
    })

    it('closes on a scroll of an inner container (capture phase)', () => {
      openAt({ bottom: 120, right: 940 })
      act(() => {
        screen.getByRole('button', { name: 'outside' }).dispatchEvent(new Event('scroll'))
      })
      expect(screen.queryByRole('menu')).toBeNull()
    })

    it('closes on a window resize', () => {
      openAt({ bottom: 120, right: 940 })
      act(() => {
        window.dispatchEvent(new Event('resize'))
      })
      expect(screen.queryByRole('menu')).toBeNull()
    })
  })
})
