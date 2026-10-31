import { isInFlight, type Status, statusWord } from '../lib/status'

export type { Status }

type Look = { pill: string; text: string; dot: string; ring: string }

// Colours and tints are the ones in knowledge/design/01-dashboard.html (STATUS)
// and 10-states-empty-errors.html. The dot's 3 px ring is the pill's own tint;
// the plain variant has the word's colour and the dot, no pill and no ring.
const looks: Record<Status, Look> = {
  running: {
    pill: 'bg-[rgba(63,207,142,0.12)]',
    text: 'text-success',
    dot: 'bg-success',
    ring: 'shadow-[0_0_0_3px_rgba(63,207,142,0.12)]',
  },
  partial: {
    pill: 'bg-[rgba(229,185,59,0.13)]',
    text: 'text-warning',
    dot: 'bg-warning',
    ring: 'shadow-[0_0_0_3px_rgba(229,185,59,0.13)]',
  },
  stopped: {
    pill: 'bg-[rgba(154,163,175,0.12)]',
    text: 'text-stopped',
    dot: 'bg-stopped',
    ring: 'shadow-[0_0_0_3px_rgba(154,163,175,0.12)]',
  },
  error: {
    pill: 'bg-[rgba(242,85,90,0.14)]',
    text: 'text-danger',
    dot: 'bg-danger',
    ring: 'shadow-[0_0_0_3px_rgba(242,85,90,0.14)]',
  },
  starting: { pill: 'bg-[rgba(76,155,245,0.14)]', text: 'text-accent', dot: '', ring: '' },
  stopping: { pill: 'bg-[rgba(76,155,245,0.14)]', text: 'text-accent', dot: '', ring: '' },
  restarting: { pill: 'bg-[rgba(76,155,245,0.14)]', text: 'text-accent', dot: '', ring: '' },
  notfound: {
    pill: 'bg-[rgba(154,163,175,0.10)]',
    text: 'text-stopped',
    dot: 'box-border border-[1.5px] border-stopped',
    ring: '',
  },
  unknown: {
    pill: 'bg-[rgba(217,199,138,0.12)]',
    text: 'text-unknown',
    dot: 'bg-[#d9c78a]',
    ring: '',
  },
}

/**
 * A status is always a dot (or spinner) plus its word, never colour alone.
 * variant="plain" drops the pill and the ring for lists that already have
 * their own rows (the scan results).
 */
export default function StatusBadge({
  status,
  variant = 'pill',
}: {
  status: Status | string
  variant?: 'pill' | 'plain'
}) {
  const key: Status = Object.hasOwn(looks, status) ? (status as Status) : 'unknown'
  const look = looks[key]
  const plain = variant === 'plain'
  const frame = plain
    ? 'text-[13px]'
    : `h-6 rounded-full px-[9px] text-[12.5px] font-medium ${look.pill}`
  return (
    <span
      className={`inline-flex w-fit items-center gap-[7px] whitespace-nowrap ${frame} ${look.text}`}
    >
      {isInFlight(key) ? (
        <span
          aria-hidden="true"
          data-testid="status-spinner"
          className="box-border size-[9px] flex-none rounded-full border-2 border-accent border-r-transparent motion-safe:animate-[spin_0.8s_linear_infinite]"
        />
      ) : (
        <span
          aria-hidden="true"
          className={`size-[7px] flex-none rounded-full ${look.dot} ${plain ? '' : look.ring}`}
        />
      )}
      <span className={key === 'notfound' ? 'line-through' : undefined}>{statusWord(key)}</span>
    </span>
  )
}
