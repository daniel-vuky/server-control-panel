export type Status = 'running' | 'partial' | 'stopped' | 'error' | 'unknown'

const styles: Record<Status, { word: string; dot: string; text: string }> = {
  running: { word: 'Running', dot: 'bg-success', text: 'text-success' },
  partial: { word: 'Partial', dot: 'bg-warning', text: 'text-warning' },
  stopped: { word: 'Stopped', dot: 'bg-muted', text: 'text-muted' },
  error: { word: 'Error', dot: 'bg-danger', text: 'text-danger' },
  unknown: { word: 'Unknown', dot: 'bg-muted', text: 'text-muted' },
}

/** A status is always a dot plus its word, never colour alone. */
export default function StatusBadge({ status }: { status: Status | string }) {
  const s = Object.hasOwn(styles, status) ? styles[status as Status] : styles.unknown
  return (
    <span className={`inline-flex items-center gap-1.5 text-[13px] font-medium ${s.text}`}>
      <span aria-hidden="true" className={`size-2 flex-none rounded-full ${s.dot}`} />
      {s.word}
    </span>
  )
}
