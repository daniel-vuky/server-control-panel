import { HardDrive } from 'lucide-react'
import { useEffect, useState } from 'react'
import { connectDrive, DOCS_URL, type DriveIssue, driveIssue } from '../../api/drive'
import type { DriveStatus } from '../../api/types.gen'
import { useLive } from '../../app/live'
import { usePageRestore } from '../../lib/usePageRestore'
import FolderPicker, { Issue } from '../settings/FolderPicker'

type Props = {
  /** How many projects were added: the return path from Google carries it. */
  added: number
  /** Later and Continue: both go on to Done. */
  onFinish(): void
}

/**
 * DriveStep is step 3 of the first-run setup (design 03): connect Google Drive
 * and choose the destination folder, or skip it. It reads the live Drive state,
 * so a return from Google lands on the connected state.
 */
export default function DriveStep({ added, onFinish }: Props) {
  const { drive: live } = useLive()
  // The answer of the last folder change, shown until the live state catches up.
  const [answer, setAnswer] = useState<DriveStatus | null>(null)
  // biome-ignore lint/correctness/useExhaustiveDependencies: a new live state replaces the answer.
  useEffect(() => setAnswer(null), [live])
  const status = answer ?? live

  const ready = status?.state === 'connected' && status.folder !== null

  return (
    <section
      aria-labelledby="drive-step-title"
      className="flex w-full max-w-[640px] flex-col gap-[22px] rounded-[14px] border border-border bg-surface p-8"
    >
      <div className="flex flex-col gap-2">
        <h1 id="drive-step-title" className="m-0 text-[22px] font-semibold tracking-tight">
          Connect Google Drive
        </h1>
        <p className="leading-relaxed text-muted">
          Database backups are compressed and uploaded to the folder you choose. The tool only
          requests access to files it creates itself.
        </p>
      </div>

      {status === null ? (
        <p role="status" className="text-[13px] text-muted">
          Loading Google Drive…
        </p>
      ) : status.state === 'not_configured' ? (
        <NotConfigured reason={status.reason} />
      ) : status.state === 'connected' ? (
        <Connected status={status} onChange={setAnswer} />
      ) : (
        <Connect added={added} />
      )}

      <div className="flex items-center gap-3">
        <button
          type="button"
          onClick={onFinish}
          className="h-[42px] cursor-pointer rounded-lg border-0 bg-transparent px-4 text-text-2 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          Later
        </button>
        {status?.state !== 'not_configured' && (
          <button
            type="button"
            disabled={!ready}
            onClick={onFinish}
            className="h-[42px] cursor-pointer rounded-lg border-0 bg-text px-[22px] font-semibold text-bg focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-default disabled:opacity-50"
          >
            Continue
          </button>
        )}
      </div>
    </section>
  )
}

function NotConfigured({ reason }: { reason: string }) {
  return (
    <div className="flex flex-col gap-1.5 text-[13px]">
      <p className="m-0 text-text-2">Google Drive is not set up on this server.</p>
      {reason !== '' && <p className="m-0 text-[12px] text-muted">{reason}</p>}
      <a
        href={DOCS_URL}
        target="_blank"
        rel="noreferrer"
        className="w-fit text-text-2 underline hover:text-white focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      >
        How to set up Google Drive
      </a>
    </div>
  )
}

/** Connect is not connected or expired: one button to Google's consent page, back to this step. */
function Connect({ added }: { added: number }) {
  const [busy, setBusy] = useState(false)
  const [issue, setIssue] = useState<DriveIssue | null>(null)
  usePageRestore(() => setBusy(false))

  const connect = async () => {
    if (busy) return
    setBusy(true)
    setIssue(null)
    try {
      const { url } = await connectDrive(`/onboarding?step=drive&added=${added}`)
      window.location.assign(url)
    } catch (err) {
      setIssue(driveIssue(err))
      setBusy(false)
    }
  }

  return (
    <div className="flex flex-col gap-3">
      <button
        type="button"
        disabled={busy}
        onClick={() => void connect()}
        className="flex h-12 cursor-pointer items-center justify-center gap-2.5 rounded-[9px] border border-border-strong bg-surface-2 font-semibold text-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-default disabled:opacity-60"
      >
        <HardDrive aria-hidden="true" size={20} strokeWidth={1.8} />
        Connect Google Drive
      </button>
      {issue && <Issue issue={issue} />}
    </div>
  )
}

function Connected({
  status,
  onChange,
}: {
  status: DriveStatus
  onChange(next: DriveStatus): void
}) {
  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center gap-3 rounded-[10px] border border-success-bg bg-success-soft px-3.5 py-3">
        <span aria-hidden="true" className="size-2 rounded-full bg-success" />
        <span>
          Connected <span className="font-mono">{status.email}</span>
        </span>
      </div>
      <FolderPicker drive={status} onChange={onChange} />
    </div>
  )
}
