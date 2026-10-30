import type { Container } from '../api/types.gen'

/**
 * ContainerTone is how a container row reads: ok while it runs, failed when it
 * crashed or keeps restarting, stopped when it ended normally or never started.
 */
export type ContainerTone = 'ok' | 'failed' | 'stopped'

/** Exit reasons of a container that stopped on its own terms (see project.ExitReason). */
const NORMAL_ENDS: ReadonlySet<string> = new Set([
  '',
  'Exited',
  'Exited (0)',
  'Created, never started',
])

/** containerTone colours a container: red for a non-zero exit, Dead and Restarting. */
export function containerTone(c: Container): ContainerTone {
  if (c.state === 'running') return 'ok'
  if (c.state === 'restarting' || c.state === 'dead') return 'failed'
  // An exit code other than 0, out of memory or an engine error.
  if ((c.state === 'exited' || c.state === 'created') && !NORMAL_ENDS.has(c.exitReason)) {
    return 'failed'
  }
  return 'stopped'
}

/** isNotCreated is a service listed with no container. */
export const isNotCreated = (c: Container) => c.state === '' && c.status === 'Not created'
