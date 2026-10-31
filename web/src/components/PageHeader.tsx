import type { ReactNode } from 'react'
import ConnectionBadge from './ConnectionBadge'

/**
 * PageHeader is the header of design 01: the title and its muted line on the
 * left, the connection badge then the page's buttons on the right.
 */
export default function PageHeader({
  title,
  subtitle,
  actions,
}: {
  title: ReactNode
  subtitle?: ReactNode
  actions?: ReactNode
}) {
  return (
    <header className="flex flex-wrap items-end justify-between gap-4">
      <div className="flex min-w-0 flex-col gap-1.5">
        <h1 className="m-0 text-2xl font-semibold tracking-tight">{title}</h1>
        {subtitle ? <p className="m-0 text-[13px] text-muted">{subtitle}</p> : null}
      </div>
      <div className="flex flex-wrap items-center gap-3">
        <ConnectionBadge />
        {actions}
      </div>
    </header>
  )
}

/** BadgeRow is the connection badge alone, right-aligned, for a page without a PageHeader. */
export function BadgeRow() {
  return (
    <div className="flex min-h-[30px] justify-end">
      <ConnectionBadge />
    </div>
  )
}
