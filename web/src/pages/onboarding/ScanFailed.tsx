import { CircleAlert } from 'lucide-react'

/** ScanFailed is the failed-scan box: the error and a button that repeats the scan. */
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
      className="flex flex-col gap-3 rounded-[10px] border border-danger/40 bg-danger/10 p-3.5"
    >
      <p className="flex items-start gap-2 text-[13px] text-text">
        <CircleAlert
          aria-hidden="true"
          size={16}
          strokeWidth={2}
          className="mt-0.5 flex-none text-danger"
        />
        <span>{error}</span>
      </p>
      <button
        type="button"
        onClick={onRetry}
        disabled={busy}
        className="h-9 w-fit cursor-pointer rounded-lg border border-border-strong px-4 text-text disabled:cursor-default disabled:opacity-50"
      >
        {label}
      </button>
    </div>
  )
}
