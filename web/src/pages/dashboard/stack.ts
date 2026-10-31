import type { Project } from '../../api/types.gen'

/**
 * stackNote is the card's stack chip (D14): "Compose · N containers", or
 * "Compose · R/N containers" when not every container runs (N counts every
 * row, Not created included). Before the first live state there are no
 * containers and it is just "Compose".
 */
export function stackNote(p: Project): string {
  const all = p.containers.length
  if (all === 0) return 'Compose'
  const running = p.containers.filter((c) => c.state === 'running').length
  if (running === all) return `Compose · ${all} ${all === 1 ? 'container' : 'containers'}`
  return `Compose · ${running}/${all} containers`
}
