import { CircleAlert } from 'lucide-react'

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
      className="flex flex-wrap items-center gap-3 rounded-lg border border-danger/40 bg-danger/10 px-4 py-3 text-text"
    >
      <CircleAlert aria-hidden="true" size={18} strokeWidth={2} className="flex-none text-danger" />
      <span className="font-semibold">{title}</span>
      <span className="text-text-2">{message}</span>
      <button
        type="button"
        onClick={onRetry}
        className="ml-auto h-8 cursor-pointer rounded-md border border-border-strong px-3 text-text"
      >
        Retry
      </button>
    </div>
  )
}
