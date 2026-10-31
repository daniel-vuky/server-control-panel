import { TriangleAlert } from 'lucide-react'

/** LoadError is the error banner of design 10, with a Retry button. */
export default function LoadError({
  title,
  message,
  onRetry,
}: {
  title: string
  message: string
  onRetry(): void
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
        className="ml-auto h-8 cursor-pointer rounded-md border border-error-strong bg-transparent px-3 text-error-fg focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      >
        Retry
      </button>
    </div>
  )
}
