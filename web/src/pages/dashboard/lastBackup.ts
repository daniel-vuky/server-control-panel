import type { LatestBackup, LatestBackups } from '../../api/types.gen'
import { formatRecent } from '../../lib/time'

/** LastBackup is a card's Last backup line: its words and the dot's tone. */
export type LastBackup = { text: string; tone: 'ok' | 'bad' | 'none' }

/** latestByProject keys the latest backups by project, or is null with no answer yet. */
export function latestByProject(data: LatestBackups | null): Map<number, LatestBackup> | null {
  return data === null ? null : new Map(data.projects.map((b) => [b.projectId, b]))
}

/**
 * lastBackupOf is a project's Last backup line (D18): the newest ended
 * backup's time, plus its state when it did not succeed, or "No backups yet".
 * It is null when there is no answer (the read failed), which shows "—".
 * `now` is for tests.
 */
export function lastBackupOf(
  byProject: Map<number, LatestBackup> | null,
  id: number,
  now?: Date,
): LastBackup | null {
  if (byProject === null) return null
  const b = byProject.get(id)
  if (b === undefined) return { text: 'No backups yet', tone: 'none' }
  const when = formatRecent(b.at, now)
  return b.state === 'ok'
    ? { text: when, tone: 'ok' }
    : { text: `${when} · ${b.state}`, tone: 'bad' }
}
