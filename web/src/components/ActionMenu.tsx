import { useCallback, useEffect, useId, useRef, useState } from 'react'
import { disabledReason } from '../lib/status'

type Props = {
  name: string
  actions: string[]
  onStart(): void
  onRestart(): void
  /** onUntrack runs the red item: Stop tracking for `untrack`, Remove from list for `remove`. */
  onUntrack?(): void
  /** status, self and dockerDown give a disabled item its reason as a title. */
  status?: string
  self?: boolean
  dockerDown?: boolean
}

/**
 * ActionMenu is the ⋯ button of design 01 and its menu of design 09. An item
 * that is not allowed stays focusable but is aria-disabled and does nothing.
 */
export default function ActionMenu({
  name,
  actions,
  onStart,
  onRestart,
  onUntrack,
  status,
  self,
  dockerDown,
}: Props) {
  const [open, setOpen] = useState(false)
  // Where the open menu sits: fixed, so the table's overflow cannot clip it.
  const [pos, setPos] = useState({ top: 0, right: 0 })
  const root = useRef<HTMLDivElement>(null)
  const trigger = useRef<HTMLButtonElement>(null)
  const menu = useRef<HTMLDivElement>(null)
  const menuId = useId()

  const items = [
    { key: 'start', label: 'Start', run: onStart },
    { key: 'restart', label: 'Restart', run: onRestart },
  ]

  // The red slot holds the one of untrack or remove the server allows, if any.
  const danger = actions.includes('untrack')
    ? 'Stop tracking'
    : actions.includes('remove')
      ? 'Remove from list'
      : null

  const close = useCallback((restoreFocus: boolean) => {
    setOpen(false)
    if (restoreFocus) trigger.current?.focus()
  }, [])

  useEffect(() => {
    if (!open) return
    menu.current?.querySelector<HTMLElement>('[role="menuitem"]')?.focus()
    const onPointerDown = (event: MouseEvent) => {
      if (root.current && !root.current.contains(event.target as Node)) close(true)
    }
    const onViewportChange = () => close(false)
    document.addEventListener('mousedown', onPointerDown)
    window.addEventListener('scroll', onViewportChange, true)
    window.addEventListener('resize', onViewportChange)
    return () => {
      document.removeEventListener('mousedown', onPointerDown)
      window.removeEventListener('scroll', onViewportChange, true)
      window.removeEventListener('resize', onViewportChange)
    }
  }, [open, close])

  const toggle = () => {
    if (!open && trigger.current) {
      const rect = trigger.current.getBoundingClientRect()
      setPos({ top: rect.bottom + 4, right: window.innerWidth - rect.right })
    }
    setOpen((v) => !v)
  }

  const onKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'Escape') {
      event.preventDefault()
      event.stopPropagation()
      close(true)
      return
    }
    if (event.key === 'Tab') {
      close(false)
      return
    }
    if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return
    event.preventDefault()
    const els = Array.from(menu.current?.querySelectorAll<HTMLElement>('[role="menuitem"]') ?? [])
    if (els.length === 0) return
    const at = els.indexOf(document.activeElement as HTMLElement)
    const step = event.key === 'ArrowDown' ? 1 : -1
    els[(at + step + els.length) % els.length].focus()
  }

  return (
    <div ref={root} className="relative">
      <button
        ref={trigger}
        type="button"
        aria-label={`More actions for ${name}`}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? menuId : undefined}
        onClick={toggle}
        className="flex size-8 cursor-pointer items-center justify-center rounded-md border-0 bg-transparent text-text-2 hover:bg-nav-active focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      >
        <svg aria-hidden="true" width="18" height="18" viewBox="0 0 24 24" fill="currentColor">
          <circle cx="5" cy="12" r="1.7" />
          <circle cx="12" cy="12" r="1.7" />
          <circle cx="19" cy="12" r="1.7" />
        </svg>
      </button>
      {open ? (
        <div
          ref={menu}
          id={menuId}
          role="menu"
          aria-label={`Actions for ${name}`}
          tabIndex={-1}
          onKeyDown={onKeyDown}
          style={{ position: 'fixed', top: pos.top, right: pos.right }}
          className="z-10 flex w-[220px] flex-col gap-0.5 rounded-[9px] border border-border-strong bg-[#181c22] p-1.5 shadow-[0_12px_32px_rgba(0,0,0,0.5)]"
        >
          {items.map((item) => {
            const allowed = actions.includes(item.key)
            return (
              <button
                key={item.key}
                type="button"
                role="menuitem"
                aria-disabled={allowed ? undefined : true}
                title={
                  allowed || status === undefined
                    ? undefined
                    : disabledReason(status, self, dockerDown)
                }
                onClick={() => {
                  if (!allowed) return
                  close(true)
                  item.run()
                }}
                className={`h-9 rounded-md border-0 bg-transparent px-2.5 text-left text-text hover:bg-[#222830] focus-visible:bg-[#222830] focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent ${allowed ? 'cursor-pointer' : 'cursor-not-allowed opacity-50'}`}
              >
                {item.label}
              </button>
            )
          })}
          {danger !== null && (
            <>
              <hr className="mx-1 my-1 h-px border-0 bg-border" />
              <button
                type="button"
                role="menuitem"
                onClick={() => {
                  close(true)
                  onUntrack?.()
                }}
                className="h-9 cursor-pointer rounded-md border-0 bg-transparent px-2.5 text-left text-danger hover:bg-[#222830] focus-visible:bg-[#222830] focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent"
              >
                {danger}
              </button>
            </>
          )}
        </div>
      ) : null}
    </div>
  )
}
