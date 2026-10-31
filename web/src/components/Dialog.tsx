import { type KeyboardEvent, type ReactNode, type RefObject, useEffect, useRef } from 'react'
import { createPortal } from 'react-dom'

const FOCUSABLE = 'button:not([disabled]), input:not([disabled]), a[href], select:not([disabled])'

type Props = {
  /** labelledBy is the id of the dialog's title. */
  labelledBy: string
  /** onClose runs on Escape and on a click on the overlay. */
  onClose(): void
  /** initialFocus is where focus starts; without it, the first focusable element. */
  initialFocus?: RefObject<HTMLElement | null>
  /** width is the dialog's width class; design 09's dialogs are 440 px. */
  width?: string
  children: ReactNode
}

/**
 * Dialog is the modal frame of designs 01 and 09: the dimmed overlay and the
 * panel. Focus starts inside and stays there; Escape and a click on the
 * overlay close it; closing returns focus to whatever opened it. It renders
 * in a portal on document.body, so a dimmed or filtered page (LiveDim) around
 * the component that opens it neither dims it nor moves its fixed overlay.
 */
export default function Dialog({
  labelledBy,
  onClose,
  initialFocus,
  width = 'w-[440px]',
  children,
}: Props) {
  const dialog = useRef<HTMLDivElement>(null)

  // biome-ignore lint/correctness/useExhaustiveDependencies: focus moves in once, when the dialog opens.
  useEffect(() => {
    const opener = document.activeElement
    const first =
      initialFocus?.current ?? dialog.current?.querySelector<HTMLElement>(FOCUSABLE) ?? null
    ;(first ?? dialog.current)?.focus()
    return () => {
      if (opener instanceof HTMLElement) opener.focus()
    }
  }, [])

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'Escape') {
      event.stopPropagation()
      onClose()
      return
    }
    if (event.key !== 'Tab') return
    const els = Array.from(dialog.current?.querySelectorAll<HTMLElement>(FOCUSABLE) ?? [])
    if (els.length === 0) return
    const first = els[0]
    const last = els[els.length - 1]
    const active = document.activeElement
    if (event.shiftKey && (active === first || active === dialog.current)) {
      event.preventDefault()
      last.focus()
    } else if (!event.shiftKey && active === last) {
      event.preventDefault()
      first.focus()
    }
  }

  return createPortal(
    <div className="fixed inset-0 z-40 flex items-start justify-center px-4 pt-[180px] pb-4">
      <button
        type="button"
        tabIndex={-1}
        aria-hidden="true"
        data-testid="confirm-overlay"
        onClick={onClose}
        className="absolute inset-0 cursor-default border-0 bg-[rgba(5,6,8,0.72)] p-0"
      />
      <div
        ref={dialog}
        role="dialog"
        aria-modal="true"
        aria-labelledby={labelledBy}
        tabIndex={-1}
        onKeyDown={onKeyDown}
        className={`relative box-border flex ${width} max-w-full flex-col gap-3.5 rounded-xl border border-border-strong bg-surface-2 p-[22px] shadow-[0_24px_64px_rgba(0,0,0,0.6)] outline-none`}
      >
        {children}
      </div>
    </div>,
    document.body,
  )
}

/** Button classes of design 09's dialogs: the outlined secondary and the light primary. */
export const SECONDARY_BUTTON =
  'h-[38px] cursor-pointer rounded-lg border border-border-strong bg-transparent px-4 text-text no-underline flex items-center focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent'
export const PRIMARY_BUTTON =
  'flex h-[38px] cursor-pointer items-center gap-[7px] rounded-lg border-0 bg-text px-[18px] font-semibold text-bg no-underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-60'
