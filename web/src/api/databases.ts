import { postJSON } from './client'
import type { AddDatabaseRequest, Project } from './types.gen'

/**
 * ADD_DATABASE_TIMEOUT_MS bounds the add request: the server spends up to
 * 15 s reading the compose config and 15 s on the check.
 */
export const ADD_DATABASE_TIMEOUT_MS = 35_000

/**
 * addDatabase checks a database inside its container and stores it as added
 * by hand; it resolves with the project as stored. A 422 carries a FieldError
 * (field type, service, name (code exists for one already added), userVar,
 * passwordVar, or check with the check's words); a 409 or 503 carries a
 * BackupRefused (db_not_running, docker_down). With no answer within
 * ADD_DATABASE_TIMEOUT_MS it rejects with a TimeoutError.
 */
export function addDatabase(projectId: number, body: AddDatabaseRequest): Promise<Project> {
  return postJSON<Project>(
    `/api/projects/${projectId}/databases`,
    body,
    AbortSignal.timeout(ADD_DATABASE_TIMEOUT_MS),
  )
}

/**
 * removeDatabase removes a database added by hand and its schedule, never a
 * backup; it resolves with the project as stored. A 409 busy means a backup
 * of it is queued or running.
 */
export function removeDatabase(projectId: number, dbId: number): Promise<Project> {
  return postJSON<Project>(`/api/projects/${projectId}/databases/${dbId}/remove`, {})
}
