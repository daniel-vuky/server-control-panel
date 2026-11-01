import { Check, TriangleAlert } from 'lucide-react'
import { useEffect, useState } from 'react'
import {
  connectDrive,
  DOCS_URL,
  type DriveIssue,
  disconnectDrive,
  driveIssue,
  TESTING_NOTE,
  testDrive,
} from '../../api/drive'
import type { DriveStatus } from '../../api/types.gen'
import { useLive } from '../../app/live'
import ConfirmDialog from '../../components/ConfirmDialog'
import { useToast } from '../../components/Toast'
import { usePageRestore } from '../../lib/usePageRestore'
import FolderPicker, { Issue } from './FolderPicker'

const buttonBase =
  'flex h-9 cursor-pointer items-center gap-2 rounded-[7px] px-3.5 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-60'
const neutralButton = `${buttonBase} border border-border-strong bg-surface-2 text-text`
const dangerButton = `${buttonBase} border border-danger-border bg-transparent text-danger-text`

function Spinner() {
  return (
    <span
      aria-hidden="true"
      className="size-3 animate-spin rounded-full border-2 border-accent border-r-transparent"
    />
  )
}

/**
 * DriveCard is the Google Drive card of Settings (design 07): the connection
 * and the destination folder, read from the live state.
 */
export default function DriveCard() {
  const { drive: live } = useLive()
  // The answer of the last change, shown until the live state catches up.
  const [answer, setAnswer] = useState<DriveStatus | null>(null)
  // biome-ignore lint/correctness/useExhaustiveDependencies: a new live state replaces the answer.
  useEffect(() => setAnswer(null), [live])
  const status = answer ?? live

  return (
    <section
      aria-labelledby="drive-title"
      className="rounded-[10px] border border-border bg-surface"
    >
      <div className="flex flex-col gap-1 border-b border-border px-[18px] py-4">
        <h2 id="drive-title" className="text-[15px] font-semibold">
          Google Drive
        </h2>
        <span className="text-[12.5px] text-muted">
          Access is limited to files created by the app. Tokens are encrypted at rest.
        </span>
      </div>
      {status === null ? (
        <p role="status" className="px-[18px] py-3.5 text-[13px] text-muted">
          Loading Google Drive…
        </p>
      ) : status.state === 'not_configured' ? (
        <NotConfigured status={status} />
      ) : status.state === 'connected' ? (
        <Connected status={status} onChange={setAnswer} />
      ) : (
        <Connect expired={status.state === 'expired'} />
      )}
    </section>
  )
}

function NotConfigured({ status }: { status: DriveStatus }) {
  return (
    <div className="flex flex-col gap-1.5 px-[18px] py-4 text-[13px]">
      <p className="m-0 text-text-2">Google Drive is not set up on this server.</p>
      {status.reason !== '' && <p className="m-0 text-[12px] text-muted">{status.reason}</p>}
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

/** Connect is the card of not_connected and expired: one button to Google's consent page. */
function Connect({ expired }: { expired: boolean }) {
  const [busy, setBusy] = useState(false)
  const [issue, setIssue] = useState<DriveIssue | null>(null)
  usePageRestore(() => setBusy(false))

  const connect = async () => {
    if (busy) return
    setBusy(true)
    setIssue(null)
    try {
      const { url } = await connectDrive('/settings')
      window.location.assign(url)
    } catch (err) {
      setIssue(driveIssue(err))
      setBusy(false)
    }
  }

  return (
    <div className="flex flex-col gap-3 px-[18px] py-4">
      {expired && (
        <div className="flex flex-col gap-1.5 rounded-lg border border-warning-border bg-warning-bg px-4 py-3 text-warning-soft">
          <p role="alert" className="m-0 flex items-center gap-3 text-[13px]">
            <TriangleAlert
              aria-hidden="true"
              size={18}
              strokeWidth={2}
              className="flex-none text-warning"
            />
            The Google Drive token has expired. Backups will fail until you reconnect.
          </p>
          <p className="m-0 text-[12px]">{TESTING_NOTE}</p>
        </div>
      )}
      <div className="flex flex-wrap items-center gap-3">
        <button
          type="button"
          disabled={busy}
          onClick={() => void connect()}
          className={`${buttonBase} border-0 bg-text font-semibold text-bg`}
        >
          {expired ? 'Reconnect Google Drive' : 'Connect Google Drive'}
        </button>
        {issue && <Issue issue={issue} />}
      </div>
    </div>
  )
}

type TestResult = { ok: true } | { ok: false; issue: DriveIssue }

function Connected({
  status,
  onChange,
}: {
  status: DriveStatus
  onChange(next: DriveStatus): void
}) {
  const toast = useToast()
  const [testing, setTesting] = useState(false)
  const [result, setResult] = useState<TestResult | null>(null)
  const [confirm, setConfirm] = useState(false)
  const [leaving, setLeaving] = useState(false)

  // A test result is about the folder it ran against.
  const folderKey = status.folder?.id ?? ''
  // biome-ignore lint/correctness/useExhaustiveDependencies: the folder changing clears the result.
  useEffect(() => setResult(null), [folderKey])

  const test = async () => {
    if (testing) return
    setTesting(true)
    setResult(null)
    try {
      const r = await testDrive()
      setResult(
        r.ok
          ? { ok: true }
          : { ok: false, issue: { code: r.code ?? '', message: r.message ?? 'The test failed.' } },
      )
    } catch (err) {
      setResult({ ok: false, issue: driveIssue(err) })
    } finally {
      setTesting(false)
    }
  }

  const disconnect = async () => {
    if (leaving) return
    setLeaving(true)
    try {
      onChange(await disconnectDrive())
      toast.success('Disconnected Google Drive')
    } catch (err) {
      const issue = driveIssue(err)
      toast.error('Could not disconnect Google Drive', issue.message)
    } finally {
      setLeaving(false)
    }
  }

  return (
    <>
      <div className="grid grid-cols-[repeat(auto-fit,minmax(min(300px,100%),1fr))] gap-4 px-[18px] py-4">
        <div className="flex flex-col gap-1.5">
          <span className="text-[12.5px] text-muted">Connected account</span>
          <span className="flex items-center gap-2">
            <span aria-hidden="true" className="size-2 rounded-full bg-success" />
            <span className="font-mono">{status.email}</span>
          </span>
        </div>
        <FolderPicker drive={status} onChange={onChange} />
      </div>

      <div className="flex flex-wrap items-center gap-2.5 px-[18px] pb-4">
        <button
          type="button"
          disabled={testing}
          onClick={() => void test()}
          className={neutralButton}
        >
          {testing && <Spinner />}
          Test connection
        </button>
        {result?.ok === true && (
          <span role="status" className="flex items-center gap-[7px] text-[13px] text-success">
            <Check aria-hidden="true" size={15} strokeWidth={2.4} />
            Uploaded a 1 KB test file to Drive and deleted it
          </span>
        )}
        {result?.ok === false && <Issue issue={result.issue} />}
        <button
          type="button"
          disabled={leaving}
          onClick={() => setConfirm(true)}
          className={`${dangerButton} ml-auto`}
        >
          Disconnect
        </button>
      </div>

      {confirm && (
        <ConfirmDialog
          title="Disconnect Google Drive?"
          body="Scheduled backups will fail until Google Drive is connected again. Files already on Drive stay there."
          confirmLabel="Disconnect"
          tone="danger"
          onCancel={() => setConfirm(false)}
          onConfirm={() => {
            setConfirm(false)
            void disconnect()
          }}
        />
      )}
    </>
  )
}
