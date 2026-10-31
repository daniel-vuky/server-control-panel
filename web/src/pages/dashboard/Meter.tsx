/** RAM_RANGE is the full RAM bar of design 01: 4 GB. */
export const RAM_RANGE = 4 * 1024 * 1024 * 1024

/**
 * Meter is a live value with the thin bar of design 01. With no value there
 * is no bar, but its track keeps its place so the value does not jump.
 */
export default function Meter({
  label,
  fraction,
  color,
  testId,
  className = '',
}: {
  label: string
  fraction: number | null
  color: string
  testId: string
  className?: string
}) {
  return (
    <span className={`flex flex-col gap-1.5 ${className}`}>
      <span className="font-mono tabular-nums">{label}</span>
      <span
        aria-hidden="true"
        className={`flex h-[3px] rounded-[2px] bg-border ${fraction === null ? 'invisible' : ''}`}
      >
        {fraction !== null && (
          <span
            data-testid={testId}
            className="rounded-[2px]"
            style={{ width: `${Math.min(Math.max(fraction, 0), 1) * 100}%`, background: color }}
          />
        )}
      </span>
    </span>
  )
}
