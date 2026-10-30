/** Every status the API can send for a project, plus the folder-gone state of a rescan. */
export type Status =
  | 'running'
  | 'partial'
  | 'stopped'
  | 'error'
  | 'unknown'
  | 'starting'
  | 'stopping'
  | 'restarting'
  | 'notfound'

const words: Record<Status, string> = {
  running: 'Running',
  partial: 'Partial',
  stopped: 'Stopped',
  error: 'Error',
  unknown: 'Unknown',
  starting: 'Starting…',
  stopping: 'Stopping…',
  restarting: 'Restarting…',
  notfound: 'Not found',
}

function isStatus(status: string): status is Status {
  return Object.hasOwn(words, status)
}

/** statusWord is the label shown for a status; a value the client does not know reads Unknown. */
export function statusWord(status: string): string {
  return isStatus(status) ? words[status] : words.unknown
}

/** isInFlight reports a status a lifecycle command is producing right now. */
export function isInFlight(status: string): boolean {
  return status === 'starting' || status === 'stopping' || status === 'restarting'
}

/** inFlightStatus is the status a project shows while the action runs. */
export function inFlightStatus(action: 'start' | 'stop' | 'restart'): Status {
  if (action === 'start') return 'starting'
  if (action === 'stop') return 'stopping'
  return 'restarting'
}

/**
 * allowedActions mirrors the server's project.Allowed: the actions a project
 * in this status may take. The Dashboard uses it for a status it set itself
 * before the next live state arrives.
 */
export function allowedActions(status: string, self: boolean, dockerUp: boolean): string[] {
  if (self || !dockerUp) return []
  switch (status) {
    case 'running':
      return ['stop', 'restart']
    case 'partial':
      return ['start', 'stop', 'restart']
    case 'stopped':
      return ['start']
    case 'error':
      return ['start', 'restart']
    default:
      return []
  }
}

/** disabledReason is the title of a control whose action is not allowed. */
export function disabledReason(status: string, self?: boolean, dockerDown?: boolean): string {
  if (isInFlight(status)) return 'Locked while a command runs'
  if (self) return 'Monitor only'
  if (dockerDown) return 'Docker is not responding'
  return `Not allowed while ${statusWord(status).replace('…', '')}`
}
