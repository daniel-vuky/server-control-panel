import ContainersTable from './ContainersTable'
import FailedComponents from './FailedComponents'
import ProjectCharts from './ProjectCharts'
import { useProjectOutlet } from './projectContext'

/**
 * ProjectOverview is the first tab: App resources (the CPU, RAM and network
 * charts), then for an Error or Partial project the components that are not
 * running, then the Containers card.
 */
export default function ProjectOverview() {
  const { project, dockerDown } = useProjectOutlet()
  const failing = project.status === 'error' || project.status === 'partial'
  return (
    <div className="flex flex-col gap-[18px]">
      <ProjectCharts project={project} />
      {failing && !dockerDown && <FailedComponents project={project} />}
      <ContainersTable containers={project.containers} dockerDown={dockerDown} />
    </div>
  )
}
