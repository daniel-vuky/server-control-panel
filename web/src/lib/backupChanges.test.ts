import { describe, expect, it } from 'vitest'
import type { Live } from '../app/live'
import { backupChanges } from './backupChanges'

const live = (over: Partial<Live>): Live => ({ backups: [], batch: null, ...over }) as Live

describe('backupChanges', () => {
  it('joins the active backups, the batch and the refusals', () => {
    const l = live({
      backups: [
        { id: 2, state: 'running' },
        { id: 5, state: 'queued' },
      ] as Live['backups'],
      batch: { id: 7, done: 3, finishedAt: '2026-10-31T02:00:00Z' } as Live['batch'],
    })
    expect(backupChanges(l, 4)).toBe('2:running,5:queued|7:3:2026-10-31T02:00:00Z|4')
  })

  it('is empty parts with no backup and no batch, and counts no refusals by default', () => {
    expect(backupChanges(live({}))).toBe('||0')
    expect(backupChanges(live({}), 2)).toBe('||2')
  })

  it('leaves an unfinished batch time empty', () => {
    const l = live({ batch: { id: 1, done: 0, finishedAt: null } as Live['batch'] })
    expect(backupChanges(l)).toBe('|1:0:|0')
  })
})
