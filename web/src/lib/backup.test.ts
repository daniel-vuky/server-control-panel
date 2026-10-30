import { describe, expect, it } from 'vitest'
import type { Backup } from '../api/types.gen'
import { makeBackup, makeDatabase, makeProject } from '../test/projects'
import { backupLine, databaseRule, projectRule, stepLabel } from './backup'

const rule = (reason: string, allowed = reason === '') => ({ allowed, reason })

describe('databaseRule', () => {
  it('allows an allowed database', () => {
    expect(databaseRule(makeDatabase({ backup: rule('') }), 'connected')).toEqual({
      enabled: true,
      drive: false,
      reason: '',
    })
  })

  it.each([
    [
      makeDatabase({ type: 'sqlite', locationKind: 'file', backup: rule('not_supported') }),
      'SQLite backups are not supported yet',
    ],
    [
      makeDatabase({ locationKind: 'host', backup: rule('not_supported') }),
      'Backups of databases outside a container are not supported yet',
    ],
    [
      makeDatabase({ type: 'redis', backup: rule('not_supported') }),
      'Backups of this database type are not supported yet',
    ],
    [makeDatabase({ backup: rule('not_supported') }), "The database's service could not be found"],
    [
      makeDatabase({ type: 'mysql', backup: rule('no_db_name') }),
      'The database name is not known. Set MYSQL_DATABASE (or MONGO_INITDB_DATABASE) on the service, then rescan.',
    ],
    [
      makeDatabase({ location: 'postgres', backup: rule('db_not_running') }),
      'Start postgres first',
    ],
    [makeDatabase({ backup: rule('not_found') }), "This project's folder is missing."],
    [makeDatabase({ backup: rule('folder_missing') }), 'Choose a destination folder in Settings.'],
    [makeDatabase({ backup: null }), 'Backups are not available for this database.'],
  ])('words a refused database: %#', (db, words) => {
    expect(databaseRule(db, 'connected')).toEqual({ enabled: false, drive: false, reason: words })
  })

  it.each(['drive_not_connected', 'drive_expired'])(
    'keeps %s enabled: it opens the Drive dialog',
    (reason) => {
      const db = makeDatabase({ backup: rule(reason) })
      expect(databaseRule(db, 'not_connected')).toEqual({
        enabled: true,
        drive: true,
        reason: 'Connect Google Drive first',
      })
    },
  )

  it('disables a Drive reason when Drive is not set up on this server', () => {
    const db = makeDatabase({ backup: rule('drive_not_connected') })
    expect(databaseRule(db, 'not_configured')).toEqual({
      enabled: false,
      drive: false,
      reason: 'Google Drive is not set up on this server.',
    })
  })
})

describe('projectRule', () => {
  it('is enabled when any database can be backed up, else gives the first reason', () => {
    const p = makeProject({
      databases: [
        makeDatabase({ name: 'a', location: 'pg', backup: rule('db_not_running') }),
        makeDatabase({ name: 'b', backup: rule('') }),
      ],
    })
    expect(projectRule(p, 'connected')).toEqual({ enabled: true, reason: '' })
    const off = makeProject({
      databases: [
        makeDatabase({ name: 'a', location: 'pg', backup: rule('db_not_running') }),
        makeDatabase({ name: 'b', locationKind: 'host', backup: rule('not_supported') }),
      ],
    })
    expect(projectRule(off, 'connected')).toEqual({ enabled: false, reason: 'Start pg first' })
  })

  it('has a reason for a project without databases', () => {
    expect(projectRule(makeProject(), 'connected')).toEqual({
      enabled: false,
      reason: 'No databases detected in this project.',
    })
  })
})

describe('stepLabel', () => {
  it('numbers the four steps as design 09 names them', () => {
    expect(['dump', 'compress', 'upload', 'prune'].map(stepLabel)).toEqual([
      '1. Dump',
      '2. Compress',
      '3. Upload to Drive',
      '4. Prune old backups',
    ])
  })
})

const steps = (states: string[], details: string[] = []) =>
  ['dump', 'compress', 'upload', 'prune'].map((step, i) => ({
    step,
    state: states[i],
    detail: details[i] ?? '',
  }))

const backup = (over: Partial<Backup>) =>
  makeBackup({ state: 'running', queuePosition: 0, ...over })

describe('backupLine', () => {
  it('says what a running backup does, with the percentage the server sent', () => {
    expect(
      backupLine(
        backup({
          steps: steps(['done', 'done', 'running', 'waiting'], ['', '', '23.6 / 38 MB · 62%']),
        }),
      ),
    ).toBe('Uploading to Drive · 62%')
    expect(backupLine(backup({ steps: steps(['running', 'waiting', 'waiting', 'waiting']) }))).toBe(
      'Dumping',
    )
  })

  it('says how many wait before a queued backup', () => {
    expect(backupLine(backup({ state: 'queued', queuePosition: 2 }))).toBe('Waiting · 2 before it')
    expect(backupLine(backup({ state: 'queued', queuePosition: 0 }))).toBe('Waiting')
  })

  it('words a finished backup', () => {
    expect(backupLine(backup({ state: 'ok' }))).toBe('Succeeded')
    expect(backupLine(backup({ state: 'failed', failedStep: 'dump', error: 'x' }))).toBe(
      'Failed at Dump · x',
    )
    expect(backupLine(backup({ state: 'refused', error: 'Needs about 1 GB' }))).toBe(
      'Refused · Needs about 1 GB',
    )
    expect(backupLine(backup({ state: 'interrupted' }))).toBe('Interrupted')
  })
})
