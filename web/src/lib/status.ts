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
