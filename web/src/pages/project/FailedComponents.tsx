import { CircleAlert } from 'lucide-react'
import { useId } from 'react'
import { Link } from 'react-router'
import type { Project } from '../../api/types.gen'
import ContainerStatus from '../../components/ContainerStatus'
import { plural } from '../../lib/plural'

/** failedComponents are the containers that do not run, and the services with no container. */
const failedComponents = (p: Project) => p.containers.filter((c) => c.state !== 'running')

/**
 * FailedComponents names each component of an Error or Partial project that
 * is not running: its status, why it stopped, how often it restarted, and a
 * link to its most recent error in Logs.
 */
export default function FailedComponents({ project }: { project: Project }) {
  const headingId = useId()
  const failed = failedComponents(project)
  if (failed.length === 0) return null

  return (
    <section
      aria-labelledby={headingId}
      className="rounded-[10px] border border-error-border bg-surface"
    >
      <div className="flex items-center gap-2 border-b border-border px-4 py-3.5">
        <CircleAlert aria-hidden="true" size={16} strokeWidth={2} className="text-danger" />
        <h2 id={headingId} className="m-0 text-[15px] font-semibold">
          Failed components
        </h2>
      </div>
      <ul className="m-0 flex list-none flex-col p-0">
        {failed.map((c) => (
          <li
            key={`${c.service}|${c.name}`}
            className="flex flex-wrap items-center gap-x-3 gap-y-2 border-b border-border px-4 py-3 text-[13px] last:border-b-0"
          >
            <span className="font-mono font-semibold break-all text-white">{c.name}</span>
            <ContainerStatus container={c} />
            {c.exitReason !== '' && c.exitReason !== c.status && (
              <span className="text-danger-text">{c.exitReason}</span>
            )}
            {c.restartCount > 0 && (
              <span className="text-muted">
                restarted {plural(c.restartCount, 'time', 'times')}
              </span>
            )}
            <Link
              to={`/projects/${project.id}/logs?container=${encodeURIComponent(c.service)}&focus=error`}
              aria-label={`View logs for ${c.name}`}
              className="ml-auto flex h-8 items-center rounded-[7px] border border-border-strong px-3 text-text no-underline hover:bg-surface-2 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
            >
              View logs
            </Link>
          </li>
        ))}
      </ul>
    </section>
  )
}
