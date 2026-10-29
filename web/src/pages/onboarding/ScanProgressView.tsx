import type { ScanProgress } from '../../api/types.gen'

/**
 * ScanProgressView is the live "Scanning… 12/30 folders" box of step 1. The
 * counts change up to 5 times a second, so the box is not a live region; a
 * static, visually hidden status announces the scan once when it appears.
 */
export default function ScanProgressView({ progress }: { progress: ScanProgress }) {
  const { examined, total, current } = progress
  const pct = total > 0 ? Math.min(100, Math.round((examined / total) * 100)) : 0
  return (
    <div className="flex flex-col gap-2.5 rounded-[10px] border border-border bg-bg p-3.5">
      <span role="status" className="sr-only">
        Scanning folders…
      </span>
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
