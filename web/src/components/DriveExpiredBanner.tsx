import { TriangleAlert } from 'lucide-react'
import { useState } from 'react'
import { useLocation } from 'react-router'
import { connectDrive, type DriveIssue, driveIssue } from '../api/drive'
import { useLive } from '../app/live'
import { usePageRestore } from '../lib/usePageRestore'

/**
 * DriveExpiredBanner is design 04's expired-token banner, shown by the app
 * shell on every page while the Drive token is expired: scheduled backups
 * fail until the owner reconnects, and Reconnect brings them back to this page.
 */
export default function DriveExpiredBanner() {
  const { drive } = useLive()
  if (drive?.state !== 'expired') return null
  return <Banner />
}

function Banner() {
  const { pathname, search } = useLocation()
  const [busy, setBusy] = useState(false)
  const [issue, setIssue] = useState<DriveIssue | null>(null)
  usePageRestore(() => setBusy(false))

  const reconnect = async () => {
    if (busy) return
    setBusy(true)
    setIssue(null)
    try {
      const { url } = await connectDrive(`${pathname}${search}`)
      window.location.assign(url)
    } catch (err) {
      setIssue(driveIssue(err))
      setBusy(false)
    }
  }

  return (
    <div
      role="alert"
      className="mb-[22px] flex flex-wrap items-center gap-3 rounded-[10px] border border-warning-border bg-warning-bg px-4 py-3 text-warning-soft"
    >
      <TriangleAlert
        aria-hidden="true"
        size={18}
        strokeWidth={2}
        className="flex-none text-warning"
      />
      <span>
        <b>Google Drive token has expired.</b> Scheduled backups will fail until you reconnect.
      </span>
      <button
        type="button"
        disabled={busy}
        onClick={() => void reconnect()}
        className="ml-auto flex h-[34px] flex-none cursor-pointer items-center rounded-[7px] border border-warning-strong bg-transparent px-3.5 font-semibold text-warning-soft focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-60"
      >
        Reconnect Google Drive
      </button>
      {issue && <span className="basis-full text-[13px] text-danger">{issue.message}</span>}
    </div>
  )
}
