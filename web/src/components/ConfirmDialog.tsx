import { type KeyboardEvent, type ReactNode, useEffect, useId, useRef } from 'react'

type Props = {
  title: ReactNode
  body: ReactNode
  /** command is shown in a box under the body; without it there is no box. */
  command?: string
  confirmLabel: string
  /** cancelLabel names the button that closes the dialog; it defaults to Cancel. */
  cancelLabel?: string
  tone: 'danger' | 'primary'
  onConfirm(): void
  onCancel(): void
}

/**
 * ConfirmDialog is the modal of designs 01 and 09. Focus starts on Cancel and
 * stays inside; Escape and a click on the overlay cancel; closing returns
 * focus to whatever opened it.
 */
export default function ConfirmDialog({
  title,
  body,
  command,
  confirmLabel,
  cancelLabel = 'Cancel',
  tone,
  onConfirm,
  onCancel,
}: Props) {
  const titleId = useId()
  const dialog = useRef<HTMLDivElement>(null)
  const cancel = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    const opener = document.activeElement
    cancel.current?.focus()
    return () => {
      if (opener instanceof HTMLElement) opener.focus()
    }
  }, [])

  const onKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'Escape') {
      event.stopPropagation()
      onCancel()
      return
    }
    if (event.key !== 'Tab') return
    const els = Array.from(dialog.current?.querySelectorAll<HTMLElement>('button') ?? [])
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

  const confirmClass =
    tone === 'danger' ? 'bg-stop px-[18px] text-white' : 'bg-text px-[18px] text-bg'

  return (
    <div className="fixed inset-0 z-40 flex items-start justify-center px-4 pt-[180px] pb-4">
      <button
        type="button"
        tabIndex={-1}
        aria-hidden="true"
        data-testid="confirm-overlay"
        onClick={onCancel}
        className="absolute inset-0 cursor-default border-0 bg-[rgba(5,6,8,0.72)] p-0"
      />
      <div
        ref={dialog}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        tabIndex={-1}
        onKeyDown={onKeyDown}
        className="relative box-border flex w-[440px] max-w-full flex-col gap-3.5 rounded-xl border border-border-strong bg-surface-2 p-[22px] shadow-[0_24px_64px_rgba(0,0,0,0.6)] outline-none"
      >
        <h2 id={titleId} className="m-0 text-[17px] font-semibold">
          {title}
        </h2>
        <p className="m-0 leading-[1.55] text-text-2">{body}</p>
        {command !== undefined && (
          <p className="m-0 rounded-lg border border-border bg-[#0f1215] px-3 py-2.5 font-mono text-[12.5px] text-muted">
            {`$ ${command} · timeout 60s`}
          </p>
        )}
        <div className="flex justify-end gap-2.5 pt-1">
          <button
            ref={cancel}
            type="button"
            onClick={onCancel}
            className="h-[38px] cursor-pointer rounded-lg border border-border-strong bg-transparent px-4 text-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          >
            {cancelLabel}
          </button>
          <button
            type="button"
            onClick={onConfirm}
            className={`h-[38px] cursor-pointer rounded-lg border-0 font-semibold focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent ${confirmClass}`}
          >
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  )
}
