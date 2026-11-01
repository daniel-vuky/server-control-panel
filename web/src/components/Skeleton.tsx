/**
 * Skeleton is one pulsing placeholder block; the caller sizes it with
 * `className`. `inline` makes it a span that sits in a line of text (a
 * header's muted line, a value after its label).
 */
export default function Skeleton({
  className = '',
  inline = false,
}: {
  className?: string
  inline?: boolean
}) {
  if (inline) {
    return (
      <span
        aria-hidden="true"
        className={`inline-block animate-scp-pulse rounded bg-border align-middle ${className}`}
      />
    )
  }
  return <div aria-hidden="true" className={`animate-scp-pulse rounded bg-border ${className}`} />
}

/** The widths SkeletonLines cycles through, so the lines read as text. */
const LINE_WIDTHS = ['w-3/5', 'w-2/5', 'w-1/2', 'w-[70%]', 'w-1/3']

/**
 * SkeletonLines is a busy region of `lines` text-high bars of varying widths,
 * for a card body or a list still being read. `label` names it for assistive
 * tech ("Loading" by default); `lineClassName` sizes each bar's height.
 */
export function SkeletonLines({
  lines = 3,
  label = 'Loading',
  className = '',
  lineClassName = 'h-3.5',
}: {
  lines?: number
  label?: string
  className?: string
  lineClassName?: string
}) {
  return (
    <div
      role="status"
      aria-busy="true"
      aria-label={label}
      className={`flex flex-col gap-2.5 ${className}`}
    >
      {Array.from({ length: lines }, (_, i) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: a fixed run of placeholder bars.
        <Skeleton key={i} className={`${lineClassName} ${LINE_WIDTHS[i % LINE_WIDTHS.length]}`} />
      ))}
    </div>
  )
}

/** SkeletonCard is the loading card of design 10: a busy region with the title, value and sub-line bars and a footer bar. `minHeight` is the card's min-height class (220 px by default; a chart card passes its own). */
export function SkeletonCard({ minHeight = 'min-h-[220px]' }: { minHeight?: string }) {
  return (
    <div
      role="status"
      aria-busy="true"
      aria-label="Loading"
      className={`box-border flex flex-col gap-3.5 rounded-xl border border-border bg-surface p-[18px] ${minHeight}`}
    >
      <Skeleton className="h-3.5 w-2/5" />
      <Skeleton className="h-[22px] w-[65%]" />
      <Skeleton className="h-3 w-1/2" />
      <div aria-hidden="true" className="mt-auto h-8 animate-scp-pulse rounded-md bg-nav-active" />
    </div>
  )
}
