import type { Live } from '../app/live'

/**
 * backupChanges is the key that moves when a backup is queued, started or
 * ended, the batch moves, or a refusal stored a row, and not on mere
 * progress. The Backups page re-reads its summary and table on it, and the
 * shell re-reads the latest backups on it (refusals, which only a page
 * counts, is 0 there).
 */
export function backupChanges(live: Live, refusals = 0): string {
  const b = live.batch
  return [
    live.backups.map((x) => `${x.id}:${x.state}`).join(','),
    b ? `${b.id}:${b.done}:${b.finishedAt ?? ''}` : '',
    refusals,
  ].join('|')
}
