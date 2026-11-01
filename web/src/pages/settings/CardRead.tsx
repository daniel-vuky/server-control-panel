import type { ReactNode } from 'react'
import LoadError from '../../components/LoadError'
import { SkeletonLines } from '../../components/Skeleton'
import Stale from '../../components/Stale'
import type { Read } from '../../lib/useRead'

/**
 * CardRead is the body of a Settings card that shows one read (spec
 * decisions 18 and 19): skeleton lines until the first answer, the error
 * banner with Retry when there is nothing to show, then the body, kept and
 * dimmed under the banner when a refresh fails. `what` finishes "Could not
 * load …" and "Loading …".
 */
export default function CardRead<T>({
  read,
  what,
  lines = 3,
  children,
}: {
  read: Read<T>
  what: string
  lines?: number
  children(data: T): ReactNode
}) {
  const title = `Could not load ${what}.`
  if (read.data === null) {
    if (read.error !== '') {
      return (
        <div className="p-3.5">
          <LoadError title={title} message={read.error} onRetry={read.refresh} />
        </div>
      )
    }
    return <SkeletonLines lines={lines} label={`Loading ${what}`} className="px-[18px] py-4" />
  }
  return (
    <Stale
      error={read.staleError}
      onRetry={read.refresh}
      title={title}
      bannerClassName="px-3.5 pt-3.5"
    >
      {children(read.data)}
    </Stale>
  )
}
