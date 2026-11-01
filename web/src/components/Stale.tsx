import type { CSSProperties, ReactNode } from 'react'
import LoadError from './LoadError'

/** STALE_STYLE is the dim of design 10 while the values on screen may be stale. */
export const STALE_STYLE: CSSProperties = { opacity: 0.55, filter: 'saturate(0.6)' }

/**
 * Stale wraps data a refresh could not renew (spec decision 19): with an
 * error it shows design 10's error banner (title, the error, Retry) above the
 * children and dims them as `LiveDim` does, so what is shown stays readable
 * but reads as old; without one it shows the children as they are. The
 * children's box is always there, so they keep their state when an error
 * comes or goes. `className` lays out that box; `bannerClassName`, when
 * given, wraps the banner (a card's padding).
 */
export default function Stale({
  error,
  onRetry,
  title,
  children,
  className,
  bannerClassName,
}: {
  error: string
  onRetry(): void
  title: string
  children: ReactNode
  className?: string
  bannerClassName?: string
}) {
  const stale = error !== ''
  const banner = stale && <LoadError title={title} message={error} onRetry={onRetry} />
  return (
    <>
      {banner && bannerClassName !== undefined ? (
        <div className={bannerClassName}>{banner}</div>
      ) : (
        banner
      )}
      <div
        data-stale={stale ? '' : undefined}
        className={className}
        style={stale ? STALE_STYLE : undefined}
      >
        {children}
      </div>
    </>
  )
}
