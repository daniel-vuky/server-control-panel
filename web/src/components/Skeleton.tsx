/** Skeleton is one pulsing placeholder block; the caller sizes it with `className`. */
export default function Skeleton({ className = '' }: { className?: string }) {
  return <div aria-hidden="true" className={`animate-scp-pulse rounded bg-border ${className}`} />
}

/** SkeletonCard is the loading card of design 10: a busy region with the title, value and sub-line bars and a footer bar. */
export function SkeletonCard() {
  return (
    <div
      role="status"
      aria-busy="true"
      aria-label="Loading"
      className="box-border flex min-h-[220px] flex-col gap-3.5 rounded-xl border border-border bg-surface p-[18px]"
    >
      <Skeleton className="h-3.5 w-2/5" />
      <Skeleton className="h-[22px] w-[65%]" />
      <Skeleton className="h-3 w-1/2" />
      <div aria-hidden="true" className="mt-auto h-8 animate-scp-pulse rounded-md bg-nav-active" />
    </div>
  )
}
