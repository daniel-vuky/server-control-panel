import type { ScanProgress } from '../../api/types.gen'

/** ScanProgressView is the live "Scanning… 12/30 folders" box of step 1. */
export default function ScanProgressView({ progress }: { progress: ScanProgress }) {
  const { examined, total, current } = progress
  const pct = total > 0 ? Math.min(100, Math.round((examined / total) * 100)) : 0
  return (
    <div
      role="status"
      className="flex flex-col gap-2.5 rounded-[10px] border border-border bg-bg p-3.5"
    >
      <div className="flex items-center gap-2.5">
        <span
          aria-hidden="true"
          className="size-3.5 flex-none animate-spin rounded-full border-2 border-accent border-r-transparent"
        />
        <span>
          Scanning…{' '}
          <span className="font-mono">
            {examined}/{total}
          </span>{' '}
          folders
        </span>
      </div>
      <span aria-hidden="true" className="flex h-1 rounded-sm bg-border">
        <span className="rounded-sm bg-accent transition-[width]" style={{ width: `${pct}%` }} />
      </span>
      {current && (
        <span className="truncate font-mono text-xs text-muted" title={current}>
          reading {current}
        </span>
      )}
    </div>
  )
}
