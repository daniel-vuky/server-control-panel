import { ApiError, getJSON, postJSON } from './client'
import type {
  ActionResult,
  DockerCheck,
  Project,
  SettingsRequest,
  UntrackResponse,
} from './types.gen'

export async function listProjects(signal?: AbortSignal): Promise<Project[]> {
  const projects = await getJSON<Project[]>('/api/projects', signal)
  // A 2xx JSON body that is not a list is not this endpoint's answer.
  if (!Array.isArray(projects))
    throw new ApiError(200, null, 'Unexpected response from the server.')
  return projects
}

export type LifecycleAction = 'start' | 'stop' | 'restart'

/** runAction runs a lifecycle command; it resolves once the command has ended. */
export function runAction(id: number, action: LifecycleAction): Promise<ActionResult> {
  return postJSON<ActionResult>(`/api/projects/${id}/${action}`, {})
}

/** checkDocker asks the server to ping Docker now. */
export function checkDocker(): Promise<DockerCheck> {
  return postJSON<DockerCheck>('/api/docker/check', {})
}

/** untrackProject stops tracking a project, or removes a Not found one from the list. */
export function untrackProject(id: number): Promise<UntrackResponse> {
  return postJSON<UntrackResponse>(`/api/projects/${id}/untrack`, {})
}

/**
 * saveSettings replaces a project's five settings and resolves with the
 * project as stored. A 422 carries a FieldError naming the field.
 */
export function saveSettings(id: number, body: SettingsRequest): Promise<Project> {
  return postJSON<Project>(`/api/projects/${id}/settings`, body)
}
