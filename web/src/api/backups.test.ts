import { describe, expect, it, vi } from 'vitest'
import {
  backUpAll,
  getBackupSettings,
  getBackupSummary,
  listBackups,
  saveBackupDefaults,
  saveBackupSchedule,
} from './backups'

const json = (code: number, body: unknown) => new Response(JSON.stringify(body), { status: code })

function answer(body: unknown, code = 200) {
  const stub = vi.fn(async () => json(code, body))
  vi.stubGlobal('fetch', stub)
  return stub
}

const call = (stub: ReturnType<typeof vi.fn>) => {
  const [url, init] = stub.mock.calls[0] as unknown as [string, RequestInit]
  return {
    url,
    method: init.method ?? 'GET',
    body: init.body ? JSON.parse(String(init.body)) : null,
  }
}

const fields = { frequency: 'weekly', time: '03:30', weekday: 3, keep: 7, compress: false }

describe('backup schedule API', () => {
  it('reads the settings and refuses an answer without them', async () => {
    const settings = { defaults: fields, timeZone: { name: 'UTC', offset: '+00:00' } }
    let stub = answer(settings)
    await expect(getBackupSettings()).resolves.toEqual(settings)
    expect(call(stub).url).toBe('/api/backup-settings')
    stub = answer({})
    await expect(getBackupSettings()).rejects.toThrow('Unexpected response from the server.')
  })

  it('posts the defaults and a database schedule as JSON', async () => {
    let stub = answer(fields)
    await saveBackupDefaults(fields)
    expect(call(stub)).toEqual({
      url: '/api/backup-settings/defaults',
      method: 'POST',
      body: fields,
    })
    stub = answer({ id: 4 })
    await saveBackupSchedule(4, { database: 'postgresql:db:shop', ...fields })
    expect(call(stub)).toEqual({
      url: '/api/projects/4/backup-schedule',
      method: 'POST',
      body: { database: 'postgresql:db:shop', ...fields },
    })
  })

  it('starts Back up all', async () => {
    const stub = answer({ id: 2, total: 5, skipped: 1 }, 202)
    await expect(backUpAll()).resolves.toEqual({ id: 2, total: 5, skipped: 1 })
    expect(call(stub)).toEqual({ url: '/api/backups/all', method: 'POST', body: {} })
  })
})

describe('listBackups', () => {
  it('asks for the first page with no filters', async () => {
    const stub = answer({ backups: [], more: false })
    await expect(listBackups()).resolves.toEqual({ backups: [], more: false })
    expect(call(stub).url).toBe('/api/backups')
  })

  it('sends only the filters that are set', async () => {
    const stub = answer({ backups: [{ id: 1 }], more: true })
    const page = await listBackups({ project: 3, result: 'failed', from: '2026-10-01', before: 90 })
    expect(page.more).toBe(true)
    expect(call(stub).url).toBe('/api/backups?project=3&result=failed&from=2026-10-01&before=90')
  })

  it('refuses an answer that is not a page', async () => {
    answer({ more: true })
    await expect(listBackups()).rejects.toThrow('Unexpected response from the server.')
  })
})

describe('getBackupSummary', () => {
  it('fills a missing failure list and next run', async () => {
    answer({ driveBytes: 0, driveFiles: 0, driveProjects: 0, days: [], ok7d: 0, failed7d: 0 })
    const s = await getBackupSummary()
    expect(s.recentFailures).toEqual([])
    expect(s.nextRun).toBeNull()
  })
})
