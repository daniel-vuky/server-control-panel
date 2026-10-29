import { ApiError, getJSON } from './client'
import type { Workspace } from './types.gen'

export async function listWorkspaces(signal?: AbortSignal): Promise<Workspace[]> {
  const workspaces = await getJSON<Workspace[]>('/api/workspaces', signal)
  // A 2xx JSON body that is not a list is not this endpoint's answer.
  if (!Array.isArray(workspaces))
    throw new ApiError(200, null, 'Unexpected response from the server.')
  return workspaces
}
