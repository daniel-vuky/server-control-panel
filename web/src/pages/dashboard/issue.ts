import type { Project } from '../../api/types.gen'

/**
 * issueOf is what an Error card shows in place of its uptime (D17), the first
 * that applies: the highest restart count of its containers, else the first
 * container that exited with a reason, else how many of its containers run,
 * else Not running.
 */
export function issueOf(p: Project): string {
  const restarts = Math.max(0, ...p.containers.map((c) => c.restartCount))
  if (restarts > 0) return `Restarted ${restarts} ${restarts === 1 ? 'time' : 'times'}`
  const exited = p.containers.find((c) => c.exitReason !== '')
  if (exited) return `${exited.service}: ${exited.exitReason}`
  if (p.containers.length > 0) {
    const running = p.containers.filter((c) => c.state === 'running').length
    return `${running} of ${p.containers.length} containers running`
  }
  return 'Not running'
}
