import { getJSON } from './client'
import type { Project } from './types.gen'

export function listProjects(signal?: AbortSignal): Promise<Project[]> {
  return getJSON<Project[]>('/api/projects', signal)
}
