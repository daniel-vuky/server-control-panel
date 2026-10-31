import { ApiError, getJSON, postJSON } from './client'
import type {
  Backup,
  BackupAllResult,
  BackupHistory,
  BackupList,
  BackupRefused,
  BackupRequest,
  BackupScheduleInput,
  BackupSettings,
  BackupStarted,
  BackupSummary,
  LatestBackups,
  Project,
  ScheduleFields,
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

/** getLatestBackups is each tracked project's newest ended backup (D10). */
export async function getLatestBackups(signal?: AbortSignal): Promise<LatestBackups> {
  const l = await getJSON<LatestBackups>('/api/backups/latest', signal)
  if (typeof l !== 'object' || l === null || !Array.isArray(l.projects)) {
    throw new ApiError(200, null, 'Unexpected response from the server.')
  }
  return { latest: l.latest ?? null, projects: l.projects }
}

/** HISTORY_PAGE is how many backups the history reads at a time. */
export const HISTORY_PAGE = 50

/**
 * listProjectBackups is one page of a project's backups, newest first, in
 * every state: the newest page, or the one before backup id `before` (the
 * last id shown).
 */
export async function listProjectBackups(
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

const unexpected = () => new ApiError(200, null, 'Unexpected response from the server.')

/** getBackupSettings is the defaults a newly found database starts from, and the server zone. */
export async function getBackupSettings(signal?: AbortSignal): Promise<BackupSettings> {
  const s = await getJSON<BackupSettings>('/api/backup-settings', signal)
  if (
    typeof s !== 'object' ||
    s === null ||
    typeof s.defaults !== 'object' ||
    s.defaults === null ||
    typeof s.timeZone !== 'object' ||
    s.timeZone === null
  ) {
    throw unexpected()
  }
  return s
}

/** saveBackupDefaults replaces the defaults (never an existing schedule); 422 names a field. */
export function saveBackupDefaults(f: ScheduleFields): Promise<ScheduleFields> {
  return postJSON<ScheduleFields>('/api/backup-settings/defaults', f)
}

/** saveBackupSchedule replaces one database's schedule and answers the project; 422 names a field. */
export function saveBackupSchedule(
  projectId: number,
  input: BackupScheduleInput,
): Promise<Project> {
  return postJSON<Project>(`/api/projects/${projectId}/backup-schedule`, input)
}

/** backUpAll starts a Back up all batch: 202 {id, total, skipped}; 409 while one runs or Drive is not ready. */
export function backUpAll(): Promise<BackupAllResult> {
  return postJSON<BackupAllResult>('/api/backups/all', {})
}

/** BackupQuery filters GET /api/backups; from and to are YYYY-MM-DD in the server zone. */
export type BackupQuery = {
  project?: number
  result?: 'ok' | 'failed'
  from?: string
  to?: string
  before?: number
  limit?: number
}

/** listBackups is one page of every project's backups, newest first, filtered by q. */
export async function listBackups(q: BackupQuery = {}, signal?: AbortSignal): Promise<BackupList> {
  const params = new URLSearchParams()
  for (const key of ['project', 'result', 'from', 'to', 'before', 'limit'] as const) {
    const v = q[key]
    if (v !== undefined && v !== '') params.set(key, String(v))
  }
  const qs = params.toString()
  const page = await getJSON<BackupList>(qs === '' ? '/api/backups' : `/api/backups?${qs}`, signal)
  if (typeof page !== 'object' || page === null || !Array.isArray(page.backups)) throw unexpected()
  return { backups: page.backups, more: page.more === true }
}

/** getBackupSummary is the Backups page's cards: Drive usage, the last 7 days and the next run. */
export async function getBackupSummary(signal?: AbortSignal): Promise<BackupSummary> {
  const s = await getJSON<BackupSummary>('/api/backups/summary', signal)
  if (typeof s !== 'object' || s === null || !Array.isArray(s.days)) throw unexpected()
  return {
    ...s,
    recentFailures: Array.isArray(s.recentFailures) ? s.recentFailures : [],
    nextRun: s.nextRun ?? null,
  }
}
