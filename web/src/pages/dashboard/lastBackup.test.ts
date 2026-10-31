import { describe, expect, it } from 'vitest'
import type { LatestBackup } from '../../api/types.gen'
import { lastBackupOf, latestByProject } from './lastBackup'

const now = new Date(2026, 9, 31, 15, 0)
const at = (day: number, h: number, m: number) => new Date(2026, 9, day, h, m).toISOString()
const row = (projectId: number, state: string, when = at(31, 2, 0)): LatestBackup => ({
  id: projectId * 10,
  projectId,
  projectName: `p${projectId}`,
  database: 'db',
  state,
  at: when,
})

describe('lastBackupOf', () => {
  const map = new Map<number, LatestBackup>([
    [1, row(1, 'ok')],
    [2, row(2, 'failed', at(30, 2, 0))],
    [3, row(3, 'refused')],
    [4, row(4, 'interrupted')],
  ])

  it('is null when the read failed or has not answered', () => {
    expect(lastBackupOf(null, 1, now)).toBeNull()
  })

  it('words an ok backup by its time alone', () => {
    expect(lastBackupOf(map, 1, now)).toEqual({ text: '02:00 today', tone: 'ok' })
  })

  it('adds the state to a backup that did not succeed', () => {
    expect(lastBackupOf(map, 2, now)).toEqual({ text: 'Yesterday 02:00 · failed', tone: 'bad' })
    expect(lastBackupOf(map, 3, now)).toEqual({ text: '02:00 today · refused', tone: 'bad' })
    expect(lastBackupOf(map, 4, now)).toEqual({ text: '02:00 today · interrupted', tone: 'bad' })
  })

  it('says No backups yet for a project with no ended backup', () => {
    expect(lastBackupOf(map, 9, now)).toEqual({ text: 'No backups yet', tone: 'none' })
  })
})

describe('latestByProject', () => {
  it('keys the rows by project, and is null with no answer', () => {
    expect(latestByProject(null)).toBeNull()
    const m = latestByProject({ latest: row(1, 'ok'), projects: [row(1, 'ok'), row(2, 'failed')] })
    expect(m?.get(2)?.state).toBe('failed')
    expect(m?.size).toBe(2)
  })
})
