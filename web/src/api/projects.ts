import { ApiError, getJSON } from './client'
import type { Project } from './types.gen'

export async function listProjects(signal?: AbortSignal): Promise<Project[]> {
  const projects = await getJSON<Project[]>('/api/projects', signal)
  // A 2xx JSON body that is not a list is not this endpoint's answer.
  if (!Array.isArray(projects))
    throw new ApiError(200, null, 'Unexpected response from the server.')
  return projects
}
