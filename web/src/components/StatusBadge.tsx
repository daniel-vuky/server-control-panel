import { isInFlight, type Status, statusWord } from '../lib/status'

export type { Status }

type Look = { pill: string; dot: string }

// Colours and tints are the ones in knowledge/design/01-dashboard.html (STATUS)
// and 10-states-empty-errors.html. The dot's 3 px ring is the pill's own tint.
const looks: Record<Status, Look> = {
  running: {
    pill: 'bg-[rgba(63,207,142,0.12)] text-success',
    dot: 'bg-success shadow-[0_0_0_3px_rgba(63,207,142,0.12)]',
  },
  partial: {
    pill: 'bg-[rgba(229,185,59,0.13)] text-warning',
    dot: 'bg-warning shadow-[0_0_0_3px_rgba(229,185,59,0.13)]',
  },
  stopped: {
    pill: 'bg-[rgba(154,163,175,0.12)] text-stopped',
    dot: 'bg-stopped shadow-[0_0_0_3px_rgba(154,163,175,0.12)]',
  },
  error: {
    pill: 'bg-[rgba(242,85,90,0.14)] text-danger',
    dot: 'bg-danger shadow-[0_0_0_3px_rgba(242,85,90,0.14)]',
  },
  starting: { pill: 'bg-[rgba(76,155,245,0.14)] text-accent', dot: '' },
  stopping: { pill: 'bg-[rgba(76,155,245,0.14)] text-accent', dot: '' },
  restarting: { pill: 'bg-[rgba(76,155,245,0.14)] text-accent', dot: '' },
  notfound: {
    pill: 'bg-[rgba(154,163,175,0.10)] text-stopped',
    dot: 'box-border border-[1.5px] border-stopped',
  },
  unknown: {
    pill: 'bg-[rgba(217,199,138,0.12)] text-unknown',
    dot: 'bg-[#d9c78a]',
  },
}

/** A status is always a dot (or spinner) plus its word, never colour alone. */
export default function StatusBadge({ status }: { status: Status | string }) {
  const key: Status = Object.hasOwn(looks, status) ? (status as Status) : 'unknown'
  const look = looks[key]
  return (
    <span
      className={`inline-flex h-6 w-fit items-center gap-[7px] rounded-full px-[9px] text-[12.5px] font-medium whitespace-nowrap ${look.pill}`}
    >
      {isInFlight(key) ? (
        <span
          aria-hidden="true"
          data-testid="status-spinner"
          className="box-border size-[9px] flex-none rounded-full border-2 border-accent border-r-transparent motion-safe:animate-[spin_0.8s_linear_infinite]"
        />
      ) : (
        <span aria-hidden="true" className={`size-[7px] flex-none rounded-full ${look.dot}`} />
      )}
      <span className={key === 'notfound' ? 'line-through' : undefined}>{statusWord(key)}</span>
    </span>
  )
}
