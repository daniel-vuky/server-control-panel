import { ApiError, getJSON, postJSON } from './client'
import type { RescanRequest, ScanStarted, Workspace } from './types.gen'

export async function listWorkspaces(signal?: AbortSignal): Promise<Workspace[]> {
  const workspaces = await getJSON<Workspace[]>('/api/workspaces', signal)
  // A 2xx JSON body that is not a list is not this endpoint's answer.
  if (!Array.isArray(workspaces))
    throw new ApiError(200, null, 'Unexpected response from the server.')
  return workspaces
}

/**
 * startRescan starts a rescan of one workspace, or of every workspace without
 * an id. It answers 202 with the job, or 409 with a ScanBusy body while a scan runs.
 */
export function startRescan(workspaceId = 0): Promise<ScanStarted> {
  const body: RescanRequest = { workspaceId }
  return postJSON<ScanStarted>('/api/rescans', body)
}
