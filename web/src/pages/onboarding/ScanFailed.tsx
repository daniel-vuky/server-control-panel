import { TriangleAlert } from 'lucide-react'

/** ScanFailed is the failed-scan banner (design 10's error banner): the error and a button that repeats the scan. */
export default function ScanFailed({
  error,
  busy,
  onRetry,
  label = 'Scan again',
}: {
  error: string
  busy: boolean
  onRetry(): void
  label?: string
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
      <span className="min-w-0 flex-1">{error}</span>
      <button
        type="button"
        onClick={onRetry}
        disabled={busy}
        className="h-8 cursor-pointer rounded-md border border-error-strong bg-transparent px-3 text-error-fg focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-default disabled:opacity-50"
      >
        {label}
      </button>
    </div>
  )
}
