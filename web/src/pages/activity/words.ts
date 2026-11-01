import type { ActivityEntry } from '../../api/types.gen'

/** Tone is the colour family of a result pill. */
export type Tone = 'ok' | 'fail' | 'run' | 'sent' | 'info'

/** VERBS are what each recorded action reads as, after the actor and before the target. */
const VERBS: Record<string, string> = {
  'project.start': 'started',
  'project.stop': 'stopped',
  'project.restart': 'restarted',
  'project.settings': 'changed the settings of',
  'project.untrack': 'stopped tracking',
  'project.remove': 'removed from the list',
  'scan.start': 'scanned',
  'scan.track': 'started tracking projects in',
  'workspace.rescan': 'rescanned workspace',
  'workspace.remove': 'removed workspace',
  'backup.run': 'backed up',
  'backup.all': 'backed up all projects',
  'backup.schedule': 'changed the backup schedule of',
  'backup.defaults': 'changed',
  'drive.connect': 'connected Google Drive as',
  'drive.folder': 'chose the Drive folder',
  'drive.disconnect': 'disconnected Google Drive as',
  'metrics.retention': 'changed',
  'alerts.channel': 'changed the alert channel',
  'alerts.thresholds': 'changed',
  'alert.project': 'detected an error in',
  'alert.domain': 'detected a domain not answering on',
  'alert.backup': 'saw a scheduled backup fail for',
  'alert.resource': 'saw a threshold exceeded on',
  'alert.tunnel': 'saw the tunnel disconnect on',
  'alert.recovery': 'saw recovery of',
}

/** verbOf is the words between the actor and the target; an action it does not know is shown as it is. */
export function verbOf(e: ActivityEntry): string {
  if (e.action === 'backup.run' && e.result === 'running') return 'started a backup of'
  return Object.hasOwn(VERBS, e.action) ? VERBS[e.action] : e.action
}

/** SUCCEEDED are the actions whose ok result reads "Succeeded"; every other ok is "Done". */
const SUCCEEDED = new Set([
  'project.start',
  'project.stop',
  'project.restart',
  'backup.run',
  'backup.all',
])

/** LIFECYCLE are the actions whose detail line ends with how long they took. */
const LIFECYCLE = new Set(['project.start', 'project.stop', 'project.restart'])

const isAlert = (e: ActivityEntry) => e.action.startsWith('alert.')

/** pillOf is the result pill: its words and colour family. */
export function pillOf(e: ActivityEntry): { label: string; tone: Tone } {
  switch (e.result) {
    case 'ok':
      return SUCCEEDED.has(e.action)
        ? { label: 'Succeeded', tone: 'ok' }
        : { label: 'Done', tone: 'info' }
    case 'failed':
      return isAlert(e)
        ? { label: 'Not delivered', tone: 'fail' }
        : { label: 'Failed', tone: 'fail' }
    case 'timed_out':
      return { label: 'Failed', tone: 'fail' }
    case 'refused':
      return { label: 'Refused', tone: 'fail' }
    case 'interrupted':
      return { label: 'Interrupted', tone: 'fail' }
    case 'running':
      return { label: 'Running', tone: 'run' }
    case 'sent':
      return { label: 'Alert sent', tone: 'sent' }
    case 'not_sent':
      return { label: 'Not sent', tone: 'fail' }
    default:
      return { label: e.result, tone: 'info' }
  }
}

/** actorOf is who did it: the person's email, "Schedule" or "System". */
export function actorOf(e: ActivityEntry): string {
  if (e.actorKind === 'schedule') return 'Schedule'
  if (e.actorKind === 'system') return 'System'
  return e.actor || 'Someone'
}

/** duration is the time from a to b as "12s", "2m 05s" or "1h 03m"; '' when either is not a time or b is before a. */
function duration(a: string, b: string | null): string {
  if (b === null) return ''
  const ms = Date.parse(b) - Date.parse(a)
  if (Number.isNaN(ms) || ms < 0) return ''
  const s = Math.round(ms / 1000)
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ${String(s % 60).padStart(2, '0')}s`
  return `${Math.floor(m / 60)}h ${String(m % 60).padStart(2, '0')}m`
}

const capitalised = (s: string) => s.charAt(0).toUpperCase() + s.slice(1)

/**
 * detailOf is the line under the sentence: the record's reason; for a
 * lifecycle action the duration after it; for an alert that was sent, the
 * channels it went to. Empty when there is nothing to say.
 */
export function detailOf(e: ActivityEntry): string {
  const parts = e.reason === '' ? [] : [e.reason]
  if (LIFECYCLE.has(e.action)) {
    const d = duration(e.at, e.finishedAt)
    if (d !== '') parts.push(d)
  }
  if (isAlert(e) && e.result === 'sent' && e.channels.length > 0) {
    parts.push(`Sent via ${e.channels.map(capitalised).join(', ')}`)
  }
  return parts.join(' · ')
}
