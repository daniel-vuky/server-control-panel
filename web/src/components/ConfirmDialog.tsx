import { type ReactNode, useId, useRef } from 'react'
import Dialog from './Dialog'

type Props = {
  title: ReactNode
  body: ReactNode
  /** command is shown in a box under the body; without it there is no box. */
  command?: string
  confirmLabel: string
  /** cancelLabel names the button that closes the dialog; it defaults to Cancel. */
  cancelLabel?: string
  tone: 'danger' | 'primary'
  /** icon is shown above the title in its tile (design 09's Drive dialog). */
  icon?: ReactNode
  /** busy disables the confirm button while its request runs. */
  busy?: boolean
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
  icon,
  busy = false,
  onConfirm,
  onCancel,
}: Props) {
  const titleId = useId()
  const cancel = useRef<HTMLButtonElement>(null)

  const confirmClass =
    tone === 'danger' ? 'bg-stop px-[18px] text-white' : 'bg-text px-[18px] text-bg'

  return (
    <Dialog labelledBy={titleId} onClose={onCancel} initialFocus={cancel}>
      {icon !== undefined && (
        <div
          aria-hidden="true"
          className="flex size-10 items-center justify-center rounded-[10px] bg-nav-active text-text"
        >
          {icon}
        </div>
      )}
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
          disabled={busy}
          aria-busy={busy || undefined}
          className={`h-[38px] cursor-pointer rounded-lg border-0 font-semibold focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-60 ${confirmClass}`}
        >
          {confirmLabel}
        </button>
      </div>
    </Dialog>
  )
}
