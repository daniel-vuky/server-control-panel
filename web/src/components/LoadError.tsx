import { TriangleAlert } from 'lucide-react'

/**
 * LoadError is the error banner of design 10, with a Retry button. `busy`
 * (the read Retry started is on its way) disables Retry and has it read
 * "Retrying…".
 */
export default function LoadError({
  title,
  message,
  onRetry,
  busy = false,
}: {
  title: string
  message: string
  onRetry(): void
  busy?: boolean
}) {
  return (
    <div
      role="alert"
      className="flex flex-wrap items-center gap-3 rounded-lg border border-error-border bg-error-bg px-4 py-3 text-error-fg"
    >
      <TriangleAlert
        aria-hidden="true"
        size={18}
        strokeWidth={2}
        className="flex-none text-danger"
      />
      <span className="font-semibold">{title}</span>
      <span className="text-error-text">{message}</span>
      <button
        type="button"
        onClick={onRetry}
        disabled={busy}
        aria-busy={busy || undefined}
        className="ml-auto h-8 cursor-pointer rounded-md border border-error-strong bg-transparent px-3 text-error-fg focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-60"
      >
        {busy ? 'Retrying…' : 'Retry'}
      </button>
    </div>
  )
}
