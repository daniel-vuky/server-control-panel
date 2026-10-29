import { getJSON } from './client'
import type { Workspace } from './types.gen'

export function listWorkspaces(signal?: AbortSignal): Promise<Workspace[]> {
  return getJSON<Workspace[]>('/api/workspaces', signal)
}
