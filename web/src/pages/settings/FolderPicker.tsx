import { CircleAlert } from 'lucide-react'
import { type FormEvent, useId, useState } from 'react'
import {
  createDriveFolder,
  type DriveIssue,
  driveIssue,
  fieldError,
  fieldIssue,
  listDriveFolders,
  selectDriveFolder,
} from '../../api/drive'
import type { DriveFolder, DriveStatus } from '../../api/types.gen'
import FieldMessage from '../../components/FieldMessage'
import { useRead } from '../../lib/useRead'

const INPUT =
  'box-border h-[38px] w-full min-w-0 rounded-[7px] border border-border-strong bg-field px-2.5 font-mono text-[13px] text-text outline-none focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent'
const CREATE_BUTTON =
  'flex h-9 cursor-pointer items-center gap-2 rounded-[7px] border-0 bg-text px-3.5 font-semibold text-bg focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-60'

/** The select value of "+ Create new folder…". */
const CREATE = '__create__'
const DEFAULT_FOLDER_NAME = 'server-backups'

type Folders = { ok: true; folders: DriveFolder[] } | { ok: false; issue: DriveIssue }

/** loadFolders never rejects on a Drive refusal: the words and the code travel with the answer. */
async function loadFolders(signal: AbortSignal): Promise<Folders> {
  try {
    return { ok: true, folders: await listDriveFolders(signal) }
  } catch (err) {
    if (signal.aborted) throw err
    return { ok: false, issue: driveIssue(err) }
  }
}

/** Issue shows a Drive refusal: its words, and the code small and muted beside them. */
export function Issue({ issue }: { issue: DriveIssue }) {
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

/** FolderLoadError is the folder list failing: the Drive words, the code, and Retry. */
function FolderLoadError({ issue, onRetry }: { issue: DriveIssue; onRetry(): void }) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      <Issue issue={issue} />
      <button
        type="button"
        onClick={onRetry}
        className="h-8 cursor-pointer rounded-md border border-border-strong bg-transparent px-3 text-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      >
        Retry
      </button>
    </div>
  )
}

/**
 * FolderPicker is the Drive destination folder of a connected account: the
 * list to choose from, "+ Create new folder…", the Drive refusals and Retry.
 * It is shared by the Settings card and the first-run setup. A choice or a
 * creation answers with the new status through onChange, which shows it until
 * the live state catches up.
 */
export default function FolderPicker({
  drive,
  onChange,
}: {
  drive: DriveStatus
  onChange?(next: DriveStatus): void
}) {
  const ids = useId()
  const { data: load, error: loadError, refresh: retry } = useRead(loadFolders, 'folders')
  const [creating, setCreating] = useState(false)
  const [name, setName] = useState(DEFAULT_FOLDER_NAME)
  const [nameError, setNameError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [folderIssue, setFolderIssue] = useState<DriveIssue | null>(null)

  const loaded = load?.ok ? load.folders : null
  const folders: DriveFolder[] = loaded ?? []
  // The selected folder always has an option, even before the list arrives.
  const options =
    drive.folder && !folders.some((f) => f.id === drive.folder?.id)
      ? [drive.folder, ...folders]
      : folders
  // With no folder chosen and none to choose from, the name field is the way forward.
  const showCreate = creating || (drive.folder === null && loaded !== null && folders.length === 0)

  const pick = async (value: string) => {
    if (value === CREATE) {
      setCreating(true)
      return
    }
    if (value === '' || saving) return
    if (value === drive.folder?.id) {
      setCreating(false)
      return
    }
    setCreating(false)
    setFolderIssue(null)
    setSaving(true)
    try {
      onChange?.(await selectDriveFolder(value))
    } catch (err) {
      const field = fieldError(err, 'id')
      if (field === null) {
        setFolderIssue(driveIssue(err))
      } else {
        setFolderIssue({ code: field.code, message: field.error })
        // The folder is gone: the list still offering it is stale.
        retry()
      }
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
      onChange?.(next)
      retry()
    } catch (err) {
      const words = fieldIssue(err, 'name')
      if (words !== null) setNameError(words)
      else setFolderIssue(driveIssue(err))
    } finally {
      setSaving(false)
    }
  }

  const folderId = `${ids}-folder`
  const nameId = `${ids}-name`
  const selected = creating ? CREATE : (drive.folder?.id ?? '')

  return (
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
        {drive.folder === null && !creating && (
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
      {loadError !== '' && (
        <FolderLoadError issue={{ code: '', message: loadError }} onRetry={retry} />
      )}
      {load && !load.ok && <FolderLoadError issue={load.issue} onRetry={retry} />}
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
              className={`${INPUT} ${nameError ? 'border-field-error' : ''}`}
            />
            <button type="submit" disabled={saving} className={CREATE_BUTTON}>
              Create
            </button>
          </div>
          {nameError && <FieldMessage id={`${nameId}-error`} message={nameError} />}
        </form>
      )}
    </div>
  )
}
