import type { Backup, BackupStep, Database, Project } from '../api/types.gen'

/** The Drive words a backup button and the chooser use (spec §6). */
export const DRIVE_NOT_SET_UP = 'Google Drive is not set up on this server.'
const CONNECT_FIRST = 'Connect Google Drive first'

/**
 * Rule is what a Back up now control does for a database: enabled or not,
 * whether it opens the Drive dialog instead of starting, and the reason in
 * words (the tooltip of a disabled control, the line of a disabled row).
 */
export type Rule = { enabled: boolean; drive: boolean; reason: string }

const SUPPORTED = new Set(['postgresql', 'mysql', 'mongodb'])

/** notSupported words a not_supported database the way the server's refusal does. */
function notSupported(db: Database): string {
  if (db.type === 'sqlite') return 'SQLite backups are not supported yet'
  if (db.locationKind !== 'container') {
    return 'Backups of databases outside a container are not supported yet'
  }
  if (!SUPPORTED.has(db.type)) return 'Backups of this database type are not supported yet'
  return "The database's service could not be found"
}

/**
 * reasonWords is a backup rule's reason in words, for a database the server
 * refused. db_not_running names the compose service the server resolved (the
 * location may be a container name), else the location.
 */
function reasonWords(db: Database, reason: string): string {
  switch (reason) {
    case 'not_supported':
      return notSupported(db)
    case 'no_db_name':
      // PostgreSQL falls back to its default database for a placeholder, so
      // only an unusable name is refused there, and POSTGRES_DB sets it.
      if (db.type === 'postgresql') {
        return 'The database name is not usable. Set POSTGRES_DB on the service, then rescan.'
      }
      return 'The database name is not known. Set MYSQL_DATABASE (or MONGO_INITDB_DATABASE) on the service, then rescan.'
    case 'docker_down':
      return 'Docker is not responding.'
    case 'db_not_running':
      return `Start ${db.backup?.service || db.location || 'the database service'} first`
    case 'not_found':
      return "This project's folder is missing."
    case 'folder_missing':
      return 'Choose a destination folder in Settings.'
    default:
      return 'Backups are not available for this database.'
  }
}

const DRIVE_REASONS = new Set(['drive_not_connected', 'drive_expired'])

/**
 * databaseRule is the Back up now rule of one database. A Drive reason keeps
 * it enabled (it opens the Drive dialog), except when Drive is not set up on
 * this server: that dialog could not succeed.
 */
export function databaseRule(db: Database, driveState: string | undefined): Rule {
  const rule = db.backup
  if (rule?.allowed) return { enabled: true, drive: false, reason: '' }
  if (rule && DRIVE_REASONS.has(rule.reason)) {
    if (driveState === 'not_configured') {
      return { enabled: false, drive: false, reason: DRIVE_NOT_SET_UP }
    }
    return { enabled: true, drive: true, reason: CONNECT_FIRST }
  }
  return { enabled: false, drive: false, reason: reasonWords(db, rule?.reason ?? '') }
}

/**
 * projectRule is the project-wide Back up now (the detail header, the ⋯
 * menu): enabled when any database is, else the first database's reason.
 */
export function projectRule(
  p: Project,
  driveState: string | undefined,
): { enabled: boolean; reason: string } {
  if (p.databases.length === 0) {
    return { enabled: false, reason: 'No databases detected in this project.' }
  }
  const rules = p.databases.map((d) => databaseRule(d, driveState))
  if (rules.some((r) => r.enabled)) return { enabled: true, reason: '' }
  return { enabled: false, reason: rules[0].reason }
}

const STEP_LABELS: Record<string, string> = {
  dump: '1. Dump',
  compress: '2. Compress',
  upload: '3. Upload to Drive',
  prune: '4. Remove old copies',
}

/** stepLabel is a step's numbered name (the #49 acceptance criteria and the spec). */
export const stepLabel = (step: string): string => STEP_LABELS[step] ?? step

const STEP_NAMES: Record<string, string> = {
  dump: 'Dump',
  compress: 'Compress',
  upload: 'Upload',
  prune: 'Prune',
}

const STEP_DOING: Record<string, string> = {
  dump: 'Dumping',
  compress: 'Compressing',
  upload: 'Uploading to Drive',
  prune: 'Removing old copies',
}

/** percentOf is the "62%" a running step's detail ends with, or null. */
export function percentOf(step: BackupStep): number | null {
  const m = /(\d{1,3})%\s*$/.exec(step.detail)
  return m ? Math.min(100, Number(m[1])) : null
}

/**
 * DISK_WORDS is how the server's disk_short refusal words begin
 * (internal/backup/manager.go, "Needs about <need> free, …"). A stored backup
 * keeps the words, not the code, so the history matches them; change both
 * together.
 */
const DISK_WORDS = 'Needs about '

const STATE_WORDS: Record<string, string> = {
  queued: 'Waiting',
  running: 'Running',
  ok: 'Succeeded',
  failed: 'Failed',
  refused: 'Refused',
  interrupted: 'Interrupted',
}

/** stateWord is the status pill's word of a backup state. */
export const stateWord = (state: string): string => STATE_WORDS[state] ?? state

/** isActive says whether a backup is still queued or running. */
export const isActive = (b: Backup): boolean => b.state === 'queued' || b.state === 'running'

/**
 * shownSteps are a backup's steps as drawn: once it is no longer active, a
 * step still marked running is drawn failed and a waiting one skipped (a row
 * the server ended without rewriting its steps), so nothing spins next to an
 * ended backup. An active backup's steps are as the server sent them.
 */
export function shownSteps(b: Backup): BackupStep[] {
  if (isActive(b)) return b.steps
  return b.steps.map((s) => {
    if (s.state === 'running') return { ...s, state: 'failed' }
    if (s.state === 'waiting') return { ...s, state: 'skipped' }
    return s
  })
}

/** isRetryable says whether Retry is offered: failed, refused or interrupted. */
export const isRetryable = (b: Backup): boolean =>
  b.state === 'failed' || b.state === 'refused' || b.state === 'interrupted'

/** waitingLine is "Waiting · N before it" for a queued backup. */
export const waitingLine = (b: Backup): string =>
  b.queuePosition > 0 ? `Waiting · ${b.queuePosition} before it` : 'Waiting'

/**
 * backupLine is a backup in one line: what it is doing ("Uploading to Drive ·
 * 62%"), how long it waits, or how it ended.
 */
export function backupLine(b: Backup): string {
  switch (b.state) {
    case 'queued':
      return waitingLine(b)
    case 'running': {
      const step = b.steps.find((s) => s.state === 'running')
      if (!step) return 'Running'
      const doing = STEP_DOING[step.step] ?? 'Running'
      const pct = percentOf(step)
      return pct === null ? doing : `${doing} · ${pct}%`
    }
    case 'ok':
      return 'Succeeded'
    case 'failed': {
      const step = STEP_NAMES[b.failedStep]
      const head = step ? `Failed at ${step}` : 'Failed'
      return b.error ? `${head} · ${b.error.slice(0, 80)}` : head
    }
    case 'refused':
      if (b.error.startsWith(DISK_WORDS)) return 'Refused · not enough space'
      return b.error ? `Refused · ${b.error}` : 'Refused'
    default:
      return stateWord(b.state)
  }
}
