import { type ReactNode, useCallback, useEffect, useRef, useState } from 'react'
import { backupRefusal, retryBackup, startBackup } from '../api/backups'
import { connectDrive, driveIssue, fieldIssue } from '../api/drive'
import type { Backup, BackupStarted, Database, Project } from '../api/types.gen'
import { useLive } from '../app/live'
import BackupChooser from '../components/BackupChooser'
import BackupProgress from '../components/BackupProgress'
import DriveRequiredDialog from '../components/DriveRequiredDialog'
import { useToast } from '../components/Toast'
import { DRIVE_NOT_SET_UP, databaseRule } from './backup'
import { formatBytes } from './format'
import { usePageRestore } from './usePageRestore'

/** Target is the database a start or a retry is about. */
type Target = { projectId: number; key: string; name: string }

const mono = (name: string): ReactNode => <span className="font-mono">{name}</span>

/** returnTo is where Google sends the browser back to: the database tab, to continue this backup. */
export const backupReturnTo = (projectId: number, key: string): string =>
  `/projects/${projectId}/database?backup=${encodeURIComponent(key)}`

const DRIVE_CODES = new Set(['drive_not_connected', 'drive_expired'])

/**
 * ROW_CODES are the refusals that store a refused backup in the history
 * (internal/backup/manager.go's refuse, checks 4, 6 and 7). not_supported is
 * also check 3, which stores none; a re-read then finds nothing new.
 */
const ROW_CODES = new Set([
  'disk_short',
  'db_not_running',
  'docker_down',
  'no_db_name',
  'not_supported',
  'drive_not_connected',
  'drive_expired',
  'folder_missing',
])

/**
 * useBackup is the Back up now flow (spec §6), shared by the detail header,
 * the Database & Backup tab and the Dashboard menu: the chooser for several
 * databases, the Drive dialog when Drive is missing or expired, the POST with
 * a pending state, the refusal toasts and the progress dialog. Render
 * `dialogs` once.
 *
 * - start(project, dbKey?) backs up that database, the only one, or opens the chooser.
 * - choose(project, dbKey?) opens the chooser with that database chosen (and starts nothing).
 * - open(backupId) opens a backup's progress dialog.
 * - retry(backup) backs up a failed, refused or interrupted backup's database
 *   again, answered like a start (the progress dialog, or a toast).
 * - pendingKey is the database whose start is out (pendingProjectId its project);
 *   retryingId is the backup whose retry is out.
 * - refusals counts the refusals that stored a refused backup, so the history
 *   can read its newest page again when it changes.
 */
export function useBackup() {
  const live = useLive()
  const toast = useToast()
  const driveState = live.drive?.state
  const [chooser, setChooser] = useState<{ project: Project; key: string | null } | null>(null)
  const [driveAsk, setDriveAsk] = useState<Target | null>(null)
  const [connecting, setConnecting] = useState(false)
  const [openId, setOpenId] = useState<number | null>(null)
  const [pending, setPending] = useState<Target | null>(null)
  const [retrying, setRetrying] = useState<number | null>(null)
  const [refusals, setRefusals] = useState(0)
  usePageRestore(() => setConnecting(false))
  // One start or retry at a time: a second press while one is out does nothing.
  const busy = useRef(false)
  const mounted = useRef(true)

  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
    }
  }, [])

  /** settle runs a start or a retry and answers it: the progress, the Drive dialog or a toast. */
  const settle = useCallback(
    async (call: () => Promise<BackupStarted>, t: Target): Promise<void> => {
      let id: number
      try {
        id = (await call()).id
      } catch (err) {
        if (!mounted.current) return
        const refusal = backupRefusal(err)
        if (refusal !== null && ROW_CODES.has(refusal.error)) setRefusals((n) => n + 1)
        if (refusal?.error === 'already_running' && refusal.id !== null) {
          toast.show(<>A backup of {mono(t.name)} is already running.</>)
          setOpenId(refusal.id)
          return
        }
        if (refusal?.error === 'disk_short') {
          toast.error(
            'Not enough space to dump',
            `Needs about ${formatBytes(refusal.needBytes)} free, the server has ${formatBytes(refusal.freeBytes)} left. Free up some disk space and try again.`,
            { label: 'View disk usage by app', to: '/server' },
          )
          return
        }
        if (
          refusal !== null &&
          DRIVE_CODES.has(refusal.error) &&
          refusal.message !== DRIVE_NOT_SET_UP
        ) {
          // Drive went away after the button was drawn.
          setDriveAsk(t)
          return
        }
        const words =
          refusal?.message ||
          fieldIssue(err, 'database') ||
          (err instanceof Error && err.message ? err.message : 'Something went wrong.')
        toast.error(<>Couldn't back up {mono(t.name)}</>, words)
        return
      }
      if (mounted.current) setOpenId(id)
    },
    [toast],
  )

  /** run backs up one database: the Drive dialog for a Drive reason, else the POST. */
  const run = useCallback(
    async (project: Project, db: Database): Promise<void> => {
      if (busy.current) return
      const rule = databaseRule(db, driveState)
      if (!rule.enabled) return
      const t: Target = { projectId: project.id, key: db.key, name: db.name }
      if (rule.drive) {
        setDriveAsk(t)
        return
      }
      busy.current = true
      setPending(t)
      try {
        await settle(() => startBackup(project.id, db.key), t)
      } finally {
        busy.current = false
        if (mounted.current) setPending(null)
      }
    },
    [driveState, settle],
  )

  const start = useCallback(
    (project: Project, dbKey?: string) => {
      if (busy.current) return
      const dbs = project.databases
      if (dbKey === undefined && dbs.length > 1) {
        setChooser({ project, key: null })
        return
      }
      const db = dbKey === undefined ? dbs[0] : dbs.find((d) => d.key === dbKey)
      if (db !== undefined) void run(project, db)
    },
    [run],
  )

  const choose = useCallback((project: Project, dbKey?: string) => {
    if (project.databases.length === 0) return
    setChooser({ project, key: dbKey ?? null })
  }, [])

  const open = useCallback((backupId: number) => setOpenId(backupId), [])

  const retry = useCallback(
    async (b: Backup): Promise<void> => {
      if (busy.current) return
      busy.current = true
      setRetrying(b.id)
      try {
        await settle(() => retryBackup(b.id), {
          projectId: b.projectId,
          key: b.database.key,
          name: b.database.name,
        })
      } finally {
        busy.current = false
        if (mounted.current) setRetrying(null)
      }
    },
    [settle],
  )

  const connect = async (t: Target) => {
    setConnecting(true)
    try {
      const { url } = await connectDrive(backupReturnTo(t.projectId, t.key))
      window.location.assign(url)
    } catch (err) {
      if (!mounted.current) return
      toast.error('Google Drive was not connected.', driveIssue(err).message)
      setConnecting(false)
    }
  }

  // The chooser follows the live project, so its rows show the current rules.
  const chooserProject =
    chooser === null
      ? null
      : (live.projects?.find((p) => p.id === chooser.project.id) ?? chooser.project)

  const dialogs = (
    <>
      {chooser !== null && chooserProject !== null && (
        <BackupChooser
          project={chooserProject}
          initialKey={chooser.key}
          driveState={driveState}
          pending={pending !== null}
          onCancel={() => setChooser(null)}
          onConfirm={async (db) => {
            await run(chooserProject, db)
            if (mounted.current) setChooser(null)
          }}
        />
      )}
      {driveAsk !== null && (
        <DriveRequiredDialog
          name={driveAsk.name}
          busy={connecting}
          onLater={() => setDriveAsk(null)}
          onConnect={() => void connect(driveAsk)}
        />
      )}
      {openId !== null && (
        <BackupProgress
          id={openId}
          retrying={retrying !== null}
          onClose={() => setOpenId(null)}
          onRetry={(b) => void retry(b)}
        />
      )}
    </>
  )

  return {
    start,
    choose,
    open,
    retry,
    dialogs,
    pendingKey: pending?.key ?? null,
    pendingProjectId: pending?.projectId ?? null,
    retryingId: retrying,
    refusals,
  }
}

export type BackupFlow = ReturnType<typeof useBackup>
