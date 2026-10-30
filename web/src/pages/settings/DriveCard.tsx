import { Check, CircleAlert } from 'lucide-react'
import { type FormEvent, useEffect, useId, useState } from 'react'
import {
  connectDrive,
  createDriveFolder,
  DOCS_URL,
  type DriveIssue,
  disconnectDrive,
  driveIssue,
  fieldIssue,
  listDriveFolders,
  selectDriveFolder,
  TESTING_NOTE,
  testDrive,
} from '../../api/drive'
import type { DriveFolder, DriveStatus } from '../../api/types.gen'
import { useLive } from '../../app/live'
import ConfirmDialog from '../../components/ConfirmDialog'
import FieldMessage from '../../components/FieldMessage'
import LoadError from '../../components/LoadError'
import { useToast } from '../../components/Toast'
import { useDriveReturn } from '../../lib/useDriveReturn'
import { useLoad } from '../../lib/useLoad'

const buttonBase =
  'flex h-9 cursor-pointer items-center gap-2 rounded-[7px] px-3.5 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-60'
const neutralButton = `${buttonBase} border border-border-strong bg-surface-2 text-text`
const dangerButton = `${buttonBase} border border-[#6B262C] bg-transparent text-[#FF8C8F]`
const INPUT =
  'box-border h-[38px] w-full min-w-0 rounded-[7px] border border-border-strong bg-[#0f1215] px-2.5 font-mono text-[13px] text-text outline-none focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent'

/** The select value of "+ Create new folder…". */
const CREATE = '__create__'
const DEFAULT_FOLDER_NAME = 'server-backups'

/** Issue shows a Drive refusal: its words, and the code small and muted beside them. */
function Issue({ issue }: { issue: DriveIssue }) {
  return (
    <span role="alert" className="flex flex-wrap items-baseline gap-x-2 text-[13px] text-danger">
      <span className="flex items-center gap-[7px]">
        <CircleAlert aria-hidden="true" size={15} strokeWidth={2} className="flex-none" />
        {issue.message}
      </span>
      {issue.code !== '' && (
        <span className="font-mono text-[11.5px] text-muted">{issue.code}</span>
      )}
    </span>
  )
}

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

  useDriveReturn(live)

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
        className="w-fit text-accent underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
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
        <div className="flex flex-col gap-1">
          <p role="alert" className="m-0 flex items-center gap-[7px] text-[13px] text-danger">
            <CircleAlert aria-hidden="true" size={15} strokeWidth={2} className="flex-none" />
            The Google Drive token has expired. Backups will fail until you reconnect.
          </p>
          <p className="m-0 text-[12px] text-muted">{TESTING_NOTE}</p>
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
  const ids = useId()
  const { load, retry } = useLoad(listDriveFolders)
  const [creating, setCreating] = useState(false)
  const [name, setName] = useState(DEFAULT_FOLDER_NAME)
  const [nameError, setNameError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [folderIssue, setFolderIssue] = useState<DriveIssue | null>(null)
  const [testing, setTesting] = useState(false)
  const [result, setResult] = useState<TestResult | null>(null)
  const [confirm, setConfirm] = useState(false)
  const [leaving, setLeaving] = useState(false)

  const folders: DriveFolder[] = load.state === 'ok' ? load.data : []
  // The selected folder always has an option, even before the list arrives.
  const options =
    status.folder && !folders.some((f) => f.id === status.folder?.id)
      ? [status.folder, ...folders]
      : folders
  // With no folder chosen and none to choose from, the name field is the way forward.
  const showCreate =
    creating || (status.folder === null && load.state === 'ok' && folders.length === 0)

  const pick = async (value: string) => {
    if (value === CREATE) {
      setCreating(true)
      return
    }
    if (value === '' || saving) return
    setCreating(false)
    setFolderIssue(null)
    setSaving(true)
    try {
      onChange(await selectDriveFolder(value))
    } catch (err) {
      const words = fieldIssue(err, 'id')
      setFolderIssue(words === null ? driveIssue(err) : { code: 'folder_missing', message: words })
    } finally {
      setSaving(false)
    }
  }

  const create = async (e: FormEvent) => {
    e.preventDefault()
    if (saving) return
    setSaving(true)
    setNameError(null)
    setFolderIssue(null)
    try {
      const next = await createDriveFolder(name)
      setCreating(false)
      setName(DEFAULT_FOLDER_NAME)
      onChange(next)
      retry()
    } catch (err) {
      const words = fieldIssue(err, 'name')
      if (words !== null) setNameError(words)
      else setFolderIssue(driveIssue(err))
    } finally {
      setSaving(false)
    }
  }

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

  const folderId = `${ids}-folder`
  const nameId = `${ids}-name`
  const selected = creating ? CREATE : (status.folder?.id ?? '')

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
        <div className="flex flex-col gap-1.5">
          <label htmlFor={folderId} className="text-[12.5px] text-muted">
            Destination folder
          </label>
          <select
            id={folderId}
            value={selected}
            disabled={saving}
            onChange={(e) => void pick(e.target.value)}
            className={INPUT}
          >
            {status.folder === null && !creating && (
              <option value="" disabled>
                Choose a folder…
              </option>
            )}
            {options.map((f) => (
              <option key={f.id} value={f.id}>
                {`My Drive / ${f.name}`}
              </option>
            ))}
            <option value={CREATE}>+ Create new folder…</option>
          </select>
          {load.state === 'error' && (
            <LoadError title="Could not load folders." message={load.message} onRetry={retry} />
          )}
          {folderIssue && <Issue issue={folderIssue} />}
          {showCreate && (
            <form onSubmit={create} noValidate className="flex flex-col gap-1.5">
              <label htmlFor={nameId} className="text-[12.5px] text-muted">
                New folder name
              </label>
              <div className="flex gap-2">
                <input
                  id={nameId}
                  type="text"
                  value={name}
                  autoComplete="off"
                  aria-invalid={nameError ? true : undefined}
                  aria-describedby={nameError ? `${nameId}-error` : undefined}
                  onChange={(e) => {
                    setName(e.target.value)
                    setNameError(null)
                  }}
                  className={`${INPUT} ${nameError ? 'border-danger' : ''}`}
                />
                <button
                  type="submit"
                  disabled={saving}
                  className={`${buttonBase} border-0 bg-text font-semibold text-bg`}
                >
                  Create
                </button>
              </div>
              {nameError && <FieldMessage id={`${nameId}-error`} message={nameError} />}
            </form>
          )}
        </div>
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
          body="Backups stop until you connect again. Files already on Drive stay there."
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
