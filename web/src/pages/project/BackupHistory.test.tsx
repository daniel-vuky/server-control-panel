import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Backup, BackupStep, Database, DriveStatus } from '../../api/types.gen'
import { FakeEventSource } from '../../test/fakeEventSource'
import {
  makeBackup,
  makeDatabase,
  makeProject,
  pushState,
  renderProject,
} from '../../test/projects'

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status })

const connected: DriveStatus = {
  state: 'connected',
  email: 'me@example.com',
  folder: { id: 'f', name: 'server-backups' },
  reason: '',
}
const allowed = { allowed: true, reason: '', service: '' }
const HISTORY = '/api/projects/1/backups?limit=50'
const MB = 1024 * 1024

const STEPS = ['dump', 'compress', 'upload', 'prune']
const steps = (states: string[], details: string[] = []): BackupStep[] =>
  STEPS.map((step, i) => ({ step, state: states[i], detail: details[i] ?? '' }))
const at = (d: number, h: number, m: number) => new Date(2026, 9, d, h, m).toISOString()

const ok = (id: number, more: Partial<Backup> = {}) =>
  makeBackup({
    id,
    state: 'ok',
    queuePosition: 0,
    steps: steps(['done', 'done', 'done', 'skipped']),
    sizeBytes: 38 * MB,
    durationMs: 41_000,
    driveLink: `https://drive.google.com/file/d/f${id}/view`,
    fileName: `shop_shop_${id}.sql.gz`,
    createdAt: at(28, 2, 0),
    ...more,
  })

const LONG =
  "Access denied for user 'crm'. Check MYSQL_PASSWORD in the db service's environment. And then some more words."

type Answer = (url: string, init?: RequestInit) => Response | undefined

/** serve answers fetch with the first answer that matches; anything else is a 404. */
function serve(...answers: Answer[]) {
  const stub = vi.fn(async (url: string, init?: RequestInit) => {
    for (const a of answers) {
      const res = a(url, init)
      if (res) return res
    }
    return json(404, { error: 'not found' })
  })
  vi.stubGlobal('fetch', stub)
  return stub
}
const get =
  (url: string, answer: () => Response): Answer =>
  (u, init) =>
    u === url && (init?.method ?? 'GET') === 'GET' ? answer() : undefined
const post =
  (url: string, answer: () => Response): Answer =>
  (u, init) =>
    u === url && init?.method === 'POST' ? answer() : undefined
const callsTo = (stub: ReturnType<typeof vi.fn>, url: string) =>
  stub.mock.calls.filter((c) => c[0] === url)

/** open renders the tab with these databases and the live backups. */
function open(dbs: Database[], live: Backup[] = []) {
  renderProject('/projects/1/database')
  pushState([makeProject({ databases: dbs })], true, { drive: connected, backups: live })
}

const shopDb = makeDatabase({ name: 'shop', backup: allowed })
const legacyDb = makeDatabase({ type: 'mysql', name: 'legacy', backup: allowed })

const history = () => screen.findByRole('table', { name: 'Backup history' })
const bodyRows = (table: HTMLElement) => within(table).getAllByRole('row').slice(1)

beforeEach(() => {
  FakeEventSource.instances = []
  vi.stubGlobal('EventSource', FakeEventSource)
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('backup history', () => {
  it('lists every state with its words and actions, newest first', async () => {
    const running = makeBackup({
      id: 6,
      state: 'running',
      queuePosition: 0,
      createdAt: at(28, 10, 42),
      steps: steps(['done', 'done', 'running', 'waiting'], ['', '', '23.6 / 38 MB · 62%']),
    })
    const failed = makeBackup({
      id: 5,
      state: 'failed',
      failedStep: 'dump',
      error: LONG,
      durationMs: 12_000,
      queuePosition: 0,
      createdAt: at(27, 16, 12),
    })
    const refused = makeBackup({
      id: 4,
      state: 'refused',
      error:
        'Needs about 2.1 GB free, the server has 1.4 GB left. Free up some disk space and try again.',
      queuePosition: 0,
      createdAt: at(27, 2, 0),
    })
    const interrupted = makeBackup({
      id: 3,
      state: 'interrupted',
      durationMs: 62_000,
      queuePosition: 0,
      createdAt: at(26, 2, 0),
    })
    serve(
      get(HISTORY, () =>
        json(200, {
          backups: [running, failed, refused, interrupted, ok(2)],
          more: false,
        }),
      ),
    )
    open([shopDb], [running])

    const table = await history()
    const heads = within(table)
      .getAllByRole('columnheader')
      .map((h) => h.textContent)
    // One database: no Database column.
    expect(heads).toEqual(['Time', 'Size', 'Duration', 'Status', 'Triggered by', 'File'])

    const rows = bodyRows(table)
    expect(rows).toHaveLength(5)
    const cells = (r: HTMLElement) =>
      within(r)
        .getAllByRole('cell')
        .map((c) => c.textContent)

    expect(cells(rows[0]).slice(0, 5)).toEqual([
      'Oct 28 10:42',
      '—',
      '—',
      'Uploading to Drive · 62%',
      'dev@localhost',
    ])
    expect(cells(rows[1])[3]).toBe(`Failed at Dump · ${LONG.slice(0, 80)}`)
    expect(cells(rows[1])[2]).toBe('12s')
    expect(cells(rows[2])[3]).toBe('Refused · not enough space')
    expect(cells(rows[3])[3]).toBe('Interrupted')
    expect(cells(rows[3])[2]).toBe('1m 02s')
    expect(cells(rows[4]).slice(0, 5)).toEqual([
      'Oct 28 02:00',
      '38 MB',
      '41s',
      'Succeeded',
      'dev@localhost',
    ])

    // Succeeded: Open in Drive and Download; no Retry.
    const drive = within(rows[4]).getByRole('link', { name: 'Open in Drive' })
    expect(drive).toHaveAttribute('href', 'https://drive.google.com/file/d/f2/view')
    expect(drive).toHaveAttribute('target', '_blank')
    const download = within(rows[4]).getByRole('link', { name: 'Download' })
    expect(download).toHaveAttribute('href', '/api/backups/2/download')
    // No download attribute: the server sends Content-Disposition on success,
    // and a failure must open its page instead of being saved as a file.
    expect(download).not.toHaveAttribute('download')
    expect(within(rows[4]).queryByRole('button', { name: 'Retry' })).toBeNull()

    // Failed, refused and interrupted: Retry only.
    for (const r of rows.slice(1, 4)) {
      expect(within(r).getByRole('button', { name: 'Retry' })).toBeEnabled()
      expect(within(r).queryByRole('link')).toBeNull()
    }
    // Running: neither.
    expect(within(rows[0]).queryByRole('button', { name: 'Retry' })).toBeNull()
    expect(within(rows[0]).queryByRole('link')).toBeNull()
  })

  it('says Schedule for a scheduled backup', async () => {
    serve(
      get(HISTORY, () => json(200, { backups: [ok(2, { startedBy: 'schedule' })], more: false })),
    )
    open([shopDb])
    const [row] = bodyRows(await history())
    expect(within(row).getAllByRole('cell')[4]).toHaveTextContent(/^Schedule$/)
  })

  it('shows a copy removed by retention without Download or Open in Drive', async () => {
    const pruned = ok(2, { prunedAt: '2026-10-30T02:01:00Z' })
    serve(get(HISTORY, () => json(200, { backups: [ok(3), pruned], more: false })))
    open([
      makeDatabase({
        name: 'shop',
        backup: allowed,
        schedule: {
          frequency: 'daily',
          time: '02:00',
          weekday: 0,
          keep: 14,
          compress: true,
          nextRunAt: null,
        },
      }),
    ])
    const [kept, removed] = bodyRows(await history())
    const file = within(removed).getAllByRole('cell').at(-1) as HTMLElement
    expect(file).toHaveTextContent('Removed (kept 14 newest)')
    expect(within(removed).queryByRole('link')).toBeNull()
    expect(within(removed).getAllByRole('cell')[3]).toHaveTextContent('Succeeded')
    expect(within(kept).getByRole('link', { name: 'Download' })).toBeInTheDocument()
  })

  it('says when a succeeded backup could not remove old copies', async () => {
    const leftover = ok(2, {
      steps: steps(
        ['done', 'done', 'done', 'failed'],
        [
          '',
          '',
          '',
          'Could not remove 1 old copy: Google Drive is not reachable. It is retried after the next backup.',
        ],
      ),
    })
    serve(get(HISTORY, () => json(200, { backups: [leftover], more: false })))
    open([shopDb])
    const [row] = bodyRows(await history())
    expect(within(row).getAllByRole('cell')[3]).toHaveTextContent(
      'Succeeded · old copies not removed',
    )
    // The copy itself is on Drive.
    expect(within(row).getByRole('link', { name: 'Download' })).toBeInTheDocument()
  })

  it('keeps the actions column whole when the content is narrow', async () => {
    // jsdom has no layout: this pins the classes that keep the File column from
    // being clipped at about 800 px (the table no longer needs 880 px; Time and
    // Triggered by wrap; the actions cell never shrinks).
    serve(get(HISTORY, () => json(200, { backups: [ok(2)], more: false })))
    open([shopDb])
    const table = await history()
    expect(table.className).not.toMatch(/min-w-\[(8\d\d|9\d\d)px\]/)
    const [row] = bodyRows(table)
    const cells = within(row).getAllByRole('cell')
    expect(cells[0].className).not.toContain('whitespace-nowrap')
    const actions = cells.at(-1) as HTMLElement
    expect(actions.className).toContain('whitespace-nowrap')
    expect(actions.className).toContain('w-px')
  })

  it('shows the Database column when the project has more than one database', async () => {
    serve(
      get(HISTORY, () =>
        json(200, {
          backups: [ok(2, { database: { ...makeBackup().database, name: 'legacy' } }), ok(1)],
          more: false,
        }),
      ),
    )
    open([shopDb, legacyDb])
    const table = await history()
    expect(
      within(table)
        .getAllByRole('columnheader')
        .map((h) => h.textContent),
    ).toEqual(['Time', 'Database', 'Size', 'Duration', 'Status', 'Triggered by', 'File'])
    const rows = bodyRows(table)
    expect(within(rows[0]).getAllByRole('cell')[1]).toHaveTextContent('legacy')
    expect(within(rows[1]).getAllByRole('cell')[1]).toHaveTextContent('shop')
  })

  it('shows a backup started on this page before the list knows it', async () => {
    serve(get(HISTORY, () => json(200, { backups: [ok(2)], more: false })))
    open([shopDb])
    await history()
    pushState([makeProject({ databases: [shopDb] })], true, {
      drive: connected,
      backups: [makeBackup({ id: 3, queuePosition: 2 })],
    })
    const rows = bodyRows(await history())
    expect(rows).toHaveLength(2)
    expect(rows[0]).toHaveTextContent('Waiting · 2 before it')
  })

  it('retries a failed backup and opens the new one', async () => {
    const failed = makeBackup({ id: 5, state: 'failed', failedStep: 'upload', queuePosition: 0 })
    const stub = serve(
      get(HISTORY, () => json(200, { backups: [failed], more: false })),
      post('/api/backups/5/retry', () => json(202, { id: 12 })),
      get('/api/backups/12', () => json(200, makeBackup({ id: 12 }))),
    )
    open([shopDb])
    const [row] = bodyRows(await history())
    fireEvent.click(within(row).getByRole('button', { name: 'Retry' }))
    await screen.findByRole('dialog', { name: 'Backup shop · shop' })
    expect(callsTo(stub, '/api/backups/5/retry')).toHaveLength(1)
    expect(callsTo(stub, '/api/backups/12')).toHaveLength(1)
  })

  it('opens a row in the progress dialog', async () => {
    const failed = makeBackup({
      id: 5,
      state: 'failed',
      failedStep: 'dump',
      error: LONG,
      queuePosition: 0,
      steps: steps(['failed', 'skipped', 'skipped', 'skipped']),
    })
    serve(
      get(HISTORY, () => json(200, { backups: [failed], more: false })),
      get('/api/backups/5', () => json(200, failed)),
    )
    open([shopDb])
    const [row] = bodyRows(await history())
    fireEvent.click(within(row).getByRole('button', { name: /^Failed at Dump/ }))
    const dialog = await screen.findByRole('dialog', { name: 'Backup shop · shop' })
    // The dialog has the whole error.
    expect(dialog).toHaveTextContent(LONG)
  })

  it('shows older backups on Show older', async () => {
    const stub = serve(
      get(HISTORY, () => json(200, { backups: [ok(9), ok(8)], more: true })),
      get(`${HISTORY}&before=8`, () => json(200, { backups: [ok(7)], more: false })),
    )
    open([shopDb])
    await history()
    fireEvent.click(screen.getByRole('button', { name: 'Show older' }))
    await waitFor(() => expect(bodyRows(screen.getByRole('table'))).toHaveLength(3))
    expect(callsTo(stub, `${HISTORY}&before=8`)).toHaveLength(1)
    expect(screen.queryByRole('button', { name: 'Show older' })).toBeNull()
  })

  it('says when older backups could not be read, and tries again', async () => {
    let fail = true
    serve(
      get(HISTORY, () => json(200, { backups: [ok(9)], more: true })),
      get(`${HISTORY}&before=9`, () =>
        fail
          ? json(503, { error: 'database is locked' })
          : json(200, { backups: [ok(8)], more: false }),
      ),
    )
    open([shopDb])
    await history()
    fireEvent.click(screen.getByRole('button', { name: 'Show older' }))
    expect(await screen.findByText('database is locked')).toBeInTheDocument()
    fail = false
    fireEvent.click(screen.getByRole('button', { name: 'Show older' }))
    await waitFor(() => expect(bodyRows(screen.getByRole('table'))).toHaveLength(2))
    expect(screen.queryByText('database is locked')).toBeNull()
  })

  it('reads the list again once when an active backup of this project finishes', async () => {
    const running = makeBackup({ id: 3, state: 'running', queuePosition: 0 })
    let page: Backup[] = [ok(2)]
    const stub = serve(get(HISTORY, () => json(200, { backups: page, more: false })))
    open([shopDb], [running])
    await history()
    expect(callsTo(stub, HISTORY)).toHaveLength(1)

    const state = (backups: Backup[]) =>
      pushState([makeProject({ databases: [shopDb] })], true, { drive: connected, backups })
    // Progress events do not read the list.
    state([{ ...running, steps: steps(['running', 'waiting', 'waiting', 'waiting']) }])
    state([running])
    expect(callsTo(stub, HISTORY)).toHaveLength(1)

    // Another project's backup finishing does not either.
    state([running, makeBackup({ id: 4, projectId: 2, state: 'running', queuePosition: 0 })])
    state([running])
    expect(callsTo(stub, HISTORY)).toHaveLength(1)

    page = [ok(3), ok(2)]
    state([])
    await waitFor(() => expect(callsTo(stub, HISTORY)).toHaveLength(2))
    await waitFor(() =>
      expect(bodyRows(screen.getByRole('table'))[0]).toHaveTextContent('Succeeded'),
    )
    // Later states without it read nothing more.
    state([])
    state([])
    expect(callsTo(stub, HISTORY)).toHaveLength(2)
  })

  it('reads the list again at once when a refusal wrote a row', async () => {
    const refusedRow = makeBackup({
      id: 3,
      state: 'refused',
      error: 'Start db first',
      queuePosition: 0,
      steps: steps(['skipped', 'skipped', 'skipped', 'skipped']),
    })
    let page: Backup[] = []
    const stub = serve(
      get(HISTORY, () => json(200, { backups: page, more: false })),
      post('/api/projects/1/backups', () =>
        json(409, { error: 'db_not_running', message: 'Start db first', service: 'db' }),
      ),
    )
    open([shopDb])
    const card = await screen.findByRole('region', { name: 'Backup history' })
    await within(card).findByText('No backup yet.')
    expect(callsTo(stub, HISTORY)).toHaveLength(1)

    page = [refusedRow]
    fireEvent.click(within(card).getByRole('button', { name: 'Back up now' }))
    await waitFor(() => expect(callsTo(stub, HISTORY)).toHaveLength(2))
    const [row] = bodyRows(await history())
    expect(row).toHaveTextContent('Refused · Start db first')
  })

  it('does not read the list again for a refusal that wrote no row', async () => {
    const failed = makeBackup({ id: 2, state: 'failed', failedStep: 'upload', queuePosition: 0 })
    const stub = serve(
      get(HISTORY, () => json(200, { backups: [failed], more: false })),
      post('/api/backups/2/retry', () =>
        json(409, { error: 'not_found', message: 'This project is no longer tracked.' }),
      ),
    )
    open([shopDb])
    const [row] = bodyRows(await history())
    fireEvent.click(within(row).getByRole('button', { name: 'Retry' }))
    await screen.findByText('This project is no longer tracked.')
    expect(callsTo(stub, HISTORY)).toHaveLength(1)
  })

  it('keeps older pages when it reads the list again', async () => {
    const running = makeBackup({ id: 10, state: 'running', queuePosition: 0 })
    let first: Backup[] = [ok(9), ok(8)]
    serve(
      get(HISTORY, () => json(200, { backups: first, more: true })),
      get(`${HISTORY}&before=8`, () => json(200, { backups: [ok(7)], more: false })),
    )
    open([shopDb], [running])
    await history()
    fireEvent.click(screen.getByRole('button', { name: 'Show older' }))
    await waitFor(() => expect(bodyRows(screen.getByRole('table'))).toHaveLength(4))

    first = [ok(10), ok(9)]
    pushState([makeProject({ databases: [shopDb] })], true, { drive: connected, backups: [] })
    await waitFor(() =>
      expect(bodyRows(screen.getByRole('table'))[0]).toHaveTextContent('Succeeded'),
    )
    const ids = bodyRows(screen.getByRole('table')).map((r) => r.getAttribute('data-id'))
    expect(ids).toEqual(['10', '9', '8', '7'])
    expect(screen.queryByRole('button', { name: 'Show older' })).toBeNull()
  })

  it('says when there is no backup yet, with Back up now', async () => {
    const stub = serve(
      get(HISTORY, () => json(200, { backups: [], more: false })),
      post('/api/projects/1/backups', () => json(202, { id: 9 })),
      get('/api/backups/9', () => json(200, makeBackup({ id: 9 }))),
    )
    open([shopDb])
    const card = await screen.findByRole('region', { name: 'Backup history' })
    await within(card).findByText('No backup yet.')
    expect(within(card).queryByRole('table')).toBeNull()
    fireEvent.click(within(card).getByRole('button', { name: 'Back up now' }))
    await screen.findByRole('dialog', { name: 'Backup shop · shop' })
    expect(callsTo(stub, '/api/projects/1/backups')).toHaveLength(1)
  })

  it('offers Back up now per database when there are several', async () => {
    serve(get(HISTORY, () => json(200, { backups: [], more: false })))
    open([shopDb, legacyDb])
    const card = await screen.findByRole('region', { name: 'Backup history' })
    await within(card).findByText('No backup yet.')
    const items = within(card).getAllByRole('listitem')
    expect(items).toHaveLength(2)
    expect(items[0]).toHaveTextContent('shop')
    expect(within(items[0]).getByRole('button', { name: 'Back up now' })).toBeEnabled()
    expect(items[1]).toHaveTextContent('legacy')
    expect(within(items[1]).getByRole('button', { name: 'Back up now' })).toBeEnabled()
  })

  it('says when the list could not be read, and tries again', async () => {
    let fail = true
    serve(
      get(HISTORY, () =>
        fail
          ? json(503, { error: 'database is locked' })
          : json(200, { backups: [ok(2)], more: false }),
      ),
    )
    open([shopDb])
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Could not load the backup history.')
    expect(alert).toHaveTextContent('database is locked')
    fail = false
    fireEvent.click(within(alert).getByRole('button', { name: 'Retry' }))
    expect(bodyRows(await history())).toHaveLength(1)
  })
})
