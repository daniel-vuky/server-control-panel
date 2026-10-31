import { CloudUpload, ExternalLink, Play, RotateCw, ScrollText, Trash2 } from 'lucide-react'
import { useCallback, useEffect, useId, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Link } from 'react-router'
import { disabledReason } from '../lib/status'

type Props = {
  name: string
  actions: string[]
  onStart(): void
  onRestart(): void
  /** onUntrack runs the red item: Stop tracking for `untrack`, Remove from list for `remove`. */
  onUntrack?(): void
  /** backup is the Back up now item: whether it can run, its reason when not, and the run. */
  backup?: { enabled: boolean; reason: string; onRun(): void }
  /** status, self and dockerDown give a disabled item its reason as a title. */
  status?: string
  self?: boolean
  dockerDown?: boolean
  /** logsHref is the View logs link; with logsDisabledReason it is shown but goes nowhere. */
  logsHref?: string
  logsDisabledReason?: string
  /** detailsHref is the Open details link. */
  detailsHref?: string
}

const itemClass =
  'flex h-9 items-center gap-2.5 rounded-md border-0 bg-transparent px-2.5 text-left no-underline hover:bg-menu-hover focus-visible:bg-menu-hover focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent'

/** activateOnSpace makes Space follow a link item, as it presses a button item (a link only answers Enter). */
const activateOnSpace = (event: React.KeyboardEvent<HTMLElement>) => {
  if (event.key !== ' ') return
  event.preventDefault()
  event.currentTarget.click()
}

/** icon is design 09's 15 px menu icon. */
const icon = (Icon: typeof Play) => <Icon aria-hidden="true" size={15} strokeWidth={1.9} />

/**
 * ActionMenu is the ⋯ button of design 01 and its menu of design 09. An item
 * that is not allowed stays focusable but is aria-disabled and does nothing.
 * The open menu is fixed at viewport coordinates and renders in a portal on
 * document.body, so neither the table's overflow nor a dimmed page (LiveDim,
 * whose filter moves fixed descendants) displaces it.
 */
export default function ActionMenu({
  name,
  actions,
  onStart,
  onRestart,
  onUntrack,
  backup,
  status,
  self,
  dockerDown,
  logsHref,
  logsDisabledReason,
  detailsHref,
}: Props) {
  const [open, setOpen] = useState(false)
  // Where the open menu sits: fixed, so the table's overflow cannot clip it.
  const [pos, setPos] = useState({ top: 0, right: 0 })
  const root = useRef<HTMLDivElement>(null)
  const trigger = useRef<HTMLButtonElement>(null)
  const menu = useRef<HTMLDivElement>(null)
  const menuId = useId()

  const items: Array<{
    key: string
    label: string
    run(): void
    allowed: boolean
    reason?: string
    icon: typeof Play
  }> = [
    { key: 'start', label: 'Start', run: onStart, allowed: actions.includes('start'), icon: Play },
    {
      key: 'restart',
      label: 'Restart',
      run: onRestart,
      allowed: actions.includes('restart'),
      icon: RotateCw,
    },
  ]
  if (backup !== undefined) {
    items.push({
      key: 'backup',
      label: 'Back up now',
      run: backup.onRun,
      allowed: backup.enabled,
      reason: backup.reason,
      icon: CloudUpload,
    })
  }

  // The red slot holds the one of untrack or remove the server allows, if any.
  const danger =
    onUntrack === undefined
      ? null
      : actions.includes('untrack')
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
      const target = event.target as Node
      // The open menu is in a portal, so it is not inside the root.
      if (root.current?.contains(target) || menu.current?.contains(target)) return
      close(true)
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
      // Focus goes back to the trigger and the default Tab moves on from it.
      close(true)
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
      {open
        ? createPortal(
            <div
              ref={menu}
              id={menuId}
              role="menu"
              aria-label={`Actions for ${name}`}
              tabIndex={-1}
              onKeyDown={onKeyDown}
              style={{ position: 'fixed', top: pos.top, right: pos.right }}
              className="z-10 flex w-[220px] flex-col gap-0.5 rounded-[9px] border border-border-strong bg-menu p-1.5 shadow-[0_12px_32px_rgba(0,0,0,0.5)]"
            >
              {items.map((item) => {
                const { allowed } = item
                const reason =
                  item.reason ??
                  (status === undefined ? undefined : disabledReason(status, self, dockerDown))
                return (
                  <button
                    key={item.key}
                    type="button"
                    role="menuitem"
                    aria-disabled={allowed ? undefined : true}
                    title={allowed ? undefined : reason}
                    onClick={() => {
                      if (!allowed) return
                      close(true)
                      item.run()
                    }}
                    className={`${itemClass} text-text ${allowed ? 'cursor-pointer' : 'cursor-not-allowed opacity-50'}`}
                  >
                    {icon(item.icon)}
                    {item.label}
                  </button>
                )
              })}
              {logsHref !== undefined || logsDisabledReason !== undefined ? (
                logsDisabledReason !== undefined || logsHref === undefined ? (
                  // Not a link, so there is nothing to follow; still focusable for the arrows.
                  <span
                    role="menuitem"
                    tabIndex={-1}
                    aria-disabled="true"
                    title={logsDisabledReason}
                    className={`${itemClass} cursor-not-allowed text-text opacity-50`}
                  >
                    {icon(ScrollText)}
                    View logs
                  </span>
                ) : (
                  <Link
                    to={logsHref}
                    role="menuitem"
                    onClick={() => close(true)}
                    onKeyDown={activateOnSpace}
                    className={`${itemClass} cursor-pointer text-text`}
                  >
                    {icon(ScrollText)}
                    View logs
                  </Link>
                )
              ) : null}
              {detailsHref !== undefined && (
                <>
                  <hr className="mx-1.5 my-1 h-px border-0 bg-border-strong" />
                  <Link
                    to={detailsHref}
                    role="menuitem"
                    onClick={() => close(true)}
                    onKeyDown={activateOnSpace}
                    className={`${itemClass} cursor-pointer text-text`}
                  >
                    {icon(ExternalLink)}
                    Open details
                  </Link>
                </>
              )}
              {danger !== null && (
                <>
                  <hr className="mx-1.5 my-1 h-px border-0 bg-border-strong" />
                  <button
                    type="button"
                    role="menuitem"
                    onClick={() => {
                      close(true)
                      onUntrack?.()
                    }}
                    className={`${itemClass} cursor-pointer text-danger`}
                  >
                    {icon(Trash2)}
                    {danger}
                  </button>
                </>
              )}
            </div>,
            document.body,
          )
        : null}
    </div>
  )
}
