import { describe, expect, it } from 'vitest'
import { makeEntry } from '../../test/activity'
import { dateOf, dayLabel, parseFilters, serverToday } from './filters'
import { actorOf, detailOf, pillOf, verbOf } from './words'

const entry = makeEntry

describe('verbOf', () => {
  it.each([
    ['project.start', 'started'],
    ['project.stop', 'stopped'],
    ['project.restart', 'restarted'],
    ['project.settings', 'changed the settings of'],
    ['project.untrack', 'stopped tracking'],
    ['project.remove', 'removed'],
    ['scan.start', 'scanned'],
    ['scan.track', 'started tracking projects in'],
    ['workspace.rescan', 'rescanned workspace'],
    ['workspace.remove', 'removed workspace'],
    ['database.add', 'added database'],
    ['database.remove', 'removed database'],
    ['backup.run', 'backed up'],
    ['backup.all', 'backed up all projects'],
    ['backup.schedule', 'changed the backup schedule of'],
    ['backup.defaults', 'changed'],
    ['drive.connect', 'connected Google Drive as'],
    ['drive.folder', 'chose the Drive folder'],
    ['drive.disconnect', 'disconnected Google Drive as'],
    ['metrics.retention', 'changed'],
    ['alerts.channel', 'changed the alert channel'],
    ['alerts.thresholds', 'changed'],
    ['alert.project', 'detected an error in'],
    ['alert.domain', 'detected a domain not answering on'],
    ['alert.backup', 'saw a scheduled backup fail for'],
    ['alert.resource', 'saw a threshold exceeded on'],
    ['alert.tunnel', 'saw the tunnel disconnect on'],
    ['alert.recovery', 'saw recovery of'],
  ])('reads %s as "%s"', (action, verb) => {
    expect(verbOf(entry({ action }))).toBe(verb)
  })

  it('says a backup in progress was started', () => {
    expect(verbOf(entry({ action: 'backup.run', result: 'running' }))).toBe('started a backup of')
    expect(verbOf(entry({ action: 'backup.run', result: 'failed' }))).toBe('backed up')
  })

  it('shows the raw action for an unknown one', () => {
    expect(verbOf(entry({ action: 'cache.purge' }))).toBe('cache.purge')
    // not a property of Object.prototype either
    expect(verbOf(entry({ action: 'constructor' }))).toBe('constructor')
  })
})

describe('pillOf', () => {
  it.each(['project.start', 'project.stop', 'project.restart', 'backup.run', 'backup.all'])(
    'reads ok on %s as Succeeded',
    (action) => {
      expect(pillOf(entry({ action, result: 'ok' }))).toEqual({ label: 'Succeeded', tone: 'ok' })
    },
  )

  it.each([
    'scan.start',
    'scan.track',
    'project.settings',
    'project.untrack',
    'drive.folder',
    'alerts.channel',
    'metrics.retention',
  ])('reads ok on %s as Done', (action) => {
    expect(pillOf(entry({ action, result: 'ok' }))).toEqual({ label: 'Done', tone: 'info' })
  })

  it('words the other results', () => {
    const r = (result: string, action = 'project.stop') => pillOf(entry({ action, result }))
    expect(r('running')).toEqual({ label: 'Running', tone: 'run' })
    expect(r('failed')).toEqual({ label: 'Failed', tone: 'fail' })
    expect(r('timed_out')).toEqual({ label: 'Failed', tone: 'fail' })
    expect(r('refused')).toEqual({ label: 'Refused', tone: 'fail' })
    expect(r('interrupted')).toEqual({ label: 'Interrupted', tone: 'fail' })
    expect(r('sent', 'alert.project')).toEqual({ label: 'Alert sent', tone: 'sent' })
    expect(r('not_sent', 'alert.project')).toEqual({ label: 'Not sent', tone: 'fail' })
    expect(r('failed', 'alert.backup')).toEqual({ label: 'Not delivered', tone: 'fail' })
  })

  it('shows a result it does not know as it is', () => {
    expect(pillOf(entry({ result: 'paused' }))).toEqual({ label: 'paused', tone: 'info' })
  })
})

describe('actorOf', () => {
  it('is the email, Schedule or System by kind', () => {
    expect(actorOf(entry())).toBe('dev@company.vn')
    expect(actorOf(entry({ actor: 'schedule', actorKind: 'schedule' }))).toBe('Schedule')
    expect(actorOf(entry({ actor: 'system', actorKind: 'system' }))).toBe('System')
  })
})

describe('detailOf', () => {
  it('appends the duration of a lifecycle action', () => {
    const at = '2025-11-01T10:12:00+07:00'
    const e = (finishedAt: string | null, reason = '') =>
      detailOf(entry({ action: 'project.start', at, finishedAt, reason }))
    expect(e('2025-11-01T10:12:12+07:00')).toBe('12s')
    expect(e('2025-11-01T10:14:05+07:00')).toBe('2m 05s')
    expect(e('2025-11-01T11:03:00+07:00')).toBe('51m 00s')
    expect(e('2025-11-01T12:15:00+07:00')).toBe('2h 03m')
    expect(e('2025-11-01T10:12:12+07:00', 'Partial')).toBe('Partial · 12s')
    expect(e(null)).toBe('')
  })

  it('does not add a duration to other actions', () => {
    expect(detailOf(entry({ action: 'scan.start', reason: '30 folders' }))).toBe('30 folders')
    expect(detailOf(entry({ action: 'backup.run', reason: '84.2 MB · 41s' }))).toBe('84.2 MB · 41s')
  })

  it('appends the channels of an alert that was sent', () => {
    const alert = entry({
      action: 'alert.project',
      actorKind: 'system',
      result: 'sent',
      reason: 'shop-db-1 exited with code 1',
      channels: ['telegram', 'email'],
    })
    expect(detailOf(alert)).toBe('shop-db-1 exited with code 1 · Sent via Telegram, email')
    expect(detailOf({ ...alert, reason: '' })).toBe('Sent via Telegram, email')
    expect(detailOf({ ...alert, result: 'not_sent', channels: [] })).toBe(
      'shop-db-1 exited with code 1',
    )
    // a failed delivery already has the failed channel in its reason
    expect(detailOf({ ...alert, result: 'failed' })).toBe('shop-db-1 exited with code 1')
  })

  it('is empty with nothing to say', () => {
    expect(detailOf(entry({ action: 'project.settings' }))).toBe('')
  })
})

describe('dayLabel', () => {
  // 1 Nov 2025 is a Saturday.
  const today = '2025-11-01'
  it('words today, yesterday and older days', () => {
    expect(dayLabel('2025-11-01T10:12:00+07:00', today)).toBe('Today · 01/11')
    expect(dayLabel('2025-10-31T23:59:00+07:00', today)).toBe('Yesterday · 31/10')
    expect(dayLabel('2025-10-30T08:00:00+07:00', today)).toBe('Thu 30/10')
    expect(dayLabel('2024-02-29T08:00:00Z', today)).toBe('Thu 29/02')
  })

  it('reads yesterday across a month and a year', () => {
    expect(dayLabel('2025-02-28T12:00:00Z', '2025-03-01')).toBe('Yesterday · 28/02')
    expect(dayLabel('2025-12-31T12:00:00Z', '2026-01-01')).toBe('Yesterday · 31/12')
  })

  it('reads the server-zone date in the text, not the browser zone', () => {
    // 00:30 on 1 Nov at +07:00 is 17:30 on 31 Oct in UTC.
    const at = '2025-11-01T00:30:00+07:00'
    expect(new Date(at).toISOString().slice(0, 10)).toBe('2025-10-31')
    const now = new Date('2025-10-31T18:00:00Z')
    const zone = { name: 'Asia/Ho_Chi_Minh', offset: '+07:00' }
    expect(dayLabel(at, serverToday(now, zone))).toBe('Today · 01/11')
    expect(dateOf(at)).toBe('2025-11-01')
  })

  it('shows a text that is not a time as it is', () => {
    expect(dayLabel('later', today)).toBe('later')
  })
})

describe('serverToday', () => {
  const now = new Date('2025-10-31T18:00:00Z')
  it('is the date in the server zone', () => {
    expect(serverToday(now, { name: 'Asia/Ho_Chi_Minh', offset: '+07:00' })).toBe('2025-11-01')
    expect(serverToday(now, { name: 'UTC', offset: '+00:00' })).toBe('2025-10-31')
  })

  it('falls back to the offset of the newest record, then the offset of a zone it does not know', () => {
    expect(serverToday(now, null, '2025-10-31T20:00:00+07:00')).toBe('2025-11-01')
    expect(serverToday(now, { name: 'Not/AZone', offset: '+07:00' })).toBe('2025-11-01')
  })
})

describe('parseFilters', () => {
  const f = (q: string) => parseFilters(new URLSearchParams(q))
  it('reads the kind and the project', () => {
    expect(f('kind=backup&project=3')).toEqual({ kind: 'backup', project: 3 })
    expect(f('')).toEqual({})
  })

  it('leaves out a value that is not valid', () => {
    expect(f('kind=other&project=abc')).toEqual({})
    expect(f('project=0')).toEqual({})
    expect(f('project=-2')).toEqual({})
    expect(f('project=1.5')).toEqual({})
    expect(f('kind=')).toEqual({})
  })
})
