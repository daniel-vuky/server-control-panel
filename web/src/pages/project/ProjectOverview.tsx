import ContainersTable from './ContainersTable'
import FailedComponents from './FailedComponents'
import { useProjectOutlet } from './projectContext'

/**
 * ProjectOverview is the first tab: for an Error or Partial project the
 * components that are not running, then the Containers card. Charts (#36)
 * will sit above Containers.
 */
export default function ProjectOverview() {
  const { project, dockerDown } = useProjectOutlet()
  const failing = project.status === 'error' || project.status === 'partial'
  return (
    <div className="flex flex-col gap-[18px]">
      {failing && !dockerDown && <FailedComponents project={project} />}
      <ContainersTable containers={project.containers} dockerDown={dockerDown} />
    </div>
  )
}
