import { ApiError, getJSON, postJSON } from './client'
import type {
  Backup,
  BackupHistory,
  BackupRefused,
  BackupRequest,
  BackupStarted,
} from './types.gen'

/** startBackup queues a backup of one database of a tracked project: 202 {id}, or a refusal. */
export function startBackup(projectId: number, database: string): Promise<BackupStarted> {
  const body: BackupRequest = { database }
  return postJSON<BackupStarted>(`/api/projects/${projectId}/backups`, body)
}

/** retryBackup queues a new backup of the same database as a failed, refused or interrupted one. */
export function retryBackup(id: number): Promise<BackupStarted> {
  return postJSON<BackupStarted>(`/api/backups/${id}/retry`, {})
}

/** getBackup is one backup: an active one with its queue position, else the stored row. */
export function getBackup(id: number, signal?: AbortSignal): Promise<Backup> {
  return getJSON<Backup>(`/api/backups/${id}`, signal)
}

/** HISTORY_PAGE is how many backups the history reads at a time. */
export const HISTORY_PAGE = 50

/**
 * listBackups is one page of a project's backups, newest first, in every
 * state: the newest page, or the one before backup id `before` (the last id
 * shown).
 */
export async function listBackups(
  projectId: number,
  before?: number,
  signal?: AbortSignal,
): Promise<BackupHistory> {
  const older = before === undefined ? '' : `&before=${before}`
  const page = await getJSON<BackupHistory>(
    `/api/projects/${projectId}/backups?limit=${HISTORY_PAGE}${older}`,
    signal,
  )
  if (typeof page !== 'object' || page === null || !Array.isArray(page.backups)) {
    throw new ApiError(200, null, 'Unexpected response from the server.')
  }
  const stored = typeof page.stored === 'object' && page.stored !== null ? page.stored : {}
  return { backups: page.backups, more: page.more === true, stored }
}

/** downloadUrl is where a succeeded backup's file is streamed from Drive. */
export const downloadUrl = (id: number): string => `/api/backups/${id}/download`

/** Refusal is a 409 BackupRefused with the sizes filled in (the server omits a zero). */
export type Refusal = Required<Pick<BackupRefused, 'error' | 'message'>> & {
  id: number | null
  needBytes: number
  freeBytes: number
  service: string
}

/** backupRefusal is the 409 refusal a start or retry answered, or null for any other error. */
export function backupRefusal(err: unknown): Refusal | null {
  if (!(err instanceof ApiError) || err.status !== 409) return null
  const b = err.body as Partial<BackupRefused> | null
  if (typeof b !== 'object' || b === null || typeof b.error !== 'string') return null
  return {
    error: b.error,
    message: typeof b.message === 'string' ? b.message : '',
    id: typeof b.id === 'number' ? b.id : null,
    needBytes: typeof b.needBytes === 'number' ? b.needBytes : 0,
    freeBytes: typeof b.freeBytes === 'number' ? b.freeBytes : 0,
    service: typeof b.service === 'string' ? b.service : '',
  }
}
