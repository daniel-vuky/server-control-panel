import { useOutletContext } from 'react-router'
import type { Project } from '../../api/types.gen'
import { useLive } from '../../app/live'
import type { useProjectActions } from '../../lib/useProjectActions'

/**
 * useProjectFromLive is the tracked project with this id from the live state.
 * loading is true until the first state arrives; after it, project is null when
 * the id is not tracked (or not a valid id at all).
 */
export function useProjectFromLive(id: number | null): {
  project: Project | null
  loading: boolean
} {
  const { projects } = useLive()
  if (id === null) return { project: null, loading: false }
  if (projects === null) return { project: null, loading: true }
  return { project: projects.find((p) => p.id === id) ?? null, loading: false }
}

/** parseProjectId is the :id route param as a project id, or null when it is not one. */
export function parseProjectId(raw: string | undefined): number | null {
  if (raw === undefined || !/^[1-9][0-9]{0,15}$/.test(raw)) return null
  return Number(raw)
}

export type ProjectActions = ReturnType<typeof useProjectActions>

/**
 * ProjectOutlet is what the detail page hands each tab. project is the live
 * project with any in-between status on top; raw is it as the server last sent
 * it, which is what the actions take. The page renders actions.dialogs.
 */
export type ProjectOutlet = {
  project: Project
  raw: Project
  dockerDown: boolean
  actions: ProjectActions
}

/** useProjectOutlet reads the detail page's project in one of its tabs. */
export function useProjectOutlet(): ProjectOutlet {
  return useOutletContext<ProjectOutlet>()
}
