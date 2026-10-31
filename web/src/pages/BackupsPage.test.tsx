import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Backup, BackupStep } from '../api/types.gen'
import {
  batch,
  callsTo,
  connected,
  drive,
  json,
  listCalls,
  openBackups,
  push,
  serveBackups,
} from '../test/backupsPage'
import { FakeEventSource } from '../test/fakeEventSource'
import { showPage } from '../test/pageshow'
import { makeBackup, makeProject } from '../test/projects'

const uploading: BackupStep[] = [
  { step: 'dump', state: 'done', detail: 'pg_dump · 18s' },
  { step: 'compress', state: 'done', detail: 'gzip · 212 → 38 MB' },
  { step: 'upload', state: 'running', detail: '23.6 / 38 MB · 62%' },
  { step: 'prune', state: 'waiting', detail: '' },
]

const okRow = (id: number, more: Partial<Backup> = {}) =>
  makeBackup({
    id,
    state: 'ok',
    queuePosition: 0,
    steps: uploading.map((s) => ({ ...s, state: 'done' })),
    sizeBytes: 1024 * 1024,
    durationMs: 9000,
    createdAt: new Date(2026, 9, 30, 2, 0).toISOString(),
    ...more,
  })

const runningCrm = makeBackup({
  id: 9,
  projectId: 2,
  projectName: 'crm',
  database: { ...makeBackup().database, name: 'crm_db', key: 'postgresql:db:crm_db' },
  state: 'running',
  queuePosition: 0,
  steps: uploading,
})

const allButton = () => screen.getAllByRole('button', { name: 'Back up all now' })[0]
const batchStatus = () => screen.getByTestId('backup-all')

let assign: ReturnType<typeof vi.fn>

beforeEach(() => {
  FakeEventSource.instances = []
  vi.stubGlobal('EventSource', FakeEventSource)
  assign = vi.fn()
  vi.stubGlobal('location', { ...window.location, assign })
  try {
    localStorage.clear()
    sessionStorage.clear()
  } catch {
    // no storage in this environment
  }
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('BackupsPage header and Drive', () => {
  it('names the Drive folder when connected, with no banner', async () => {
    serveBackups([okRow(1)])
    openBackups(connected)
    expect(screen.getByRole('heading', { level: 1, name: 'Backups' })).toBeInTheDocument()
    expect(screen.getByText(/^All project database backups/)).toHaveTextContent(
      'All project database backups · stored in Drive/server-backups',
    )
    await screen.findByRole('table', { name: 'All backups' })
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('shows the heading before the live state arrives, without banners', () => {
    serveBackups()
    openBackups(null)
    expect(screen.getByRole('heading', { level: 1, name: 'Backups' })).toBeInTheDocument()
    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.queryByText(/stored in/)).toBeNull()
    expect(allButton()).toBeDisabled()
  })

  it('offers Connect Google Drive when not connected, returning here', async () => {
    const stub = serveBackups([], {
      other: (url) =>
        url === '/api/drive/connect'
          ? json(200, { url: 'https://accounts.google.com/x' })
          : undefined,
    })
    openBackups(drive('not_connected'))
    const banner = screen.getByRole('alert')
    expect(banner).toHaveTextContent('Google Drive not connected')
    expect(banner).toHaveTextContent(
      'Backups need a destination. Connect once and all projects share the same target folder.',
    )
    expect(screen.queryByText(/stored in/)).toBeNull()

    fireEvent.click(within(banner).getByRole('button', { name: 'Connect Google Drive' }))
    await waitFor(() => expect(assign).toHaveBeenCalledWith('https://accounts.google.com/x'))
    const call = stub.mock.calls.find((c) => c[0] === '/api/drive/connect') as unknown as [
      string,
      RequestInit,
    ]
    expect(JSON.parse(String(call[1].body))).toEqual({ returnTo: '/backups' })
    // Back from the consent page restores this page from the cache: Connect works again.
    expect(within(banner).getByRole('button', { name: 'Connect Google Drive' })).toBeDisabled()
    showPage(true)
    expect(within(banner).getByRole('button', { name: 'Connect Google Drive' })).toBeEnabled()
  })

  it('has no expired banner of its own: the app shell shows it on every page', () => {
    serveBackups()
    openBackups(drive('expired'))
    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.queryByText(/token has expired/)).toBeNull()
    expect(screen.queryByRole('button', { name: 'Reconnect Google Drive' })).toBeNull()
  })

  it('says why the connect failed', async () => {
    serveBackups([], {
      other: (url) =>
        url === '/api/drive/connect'
          ? json(503, { error: 'unreachable', message: 'Google Drive is unreachable.' })
          : undefined,
    })
    openBackups(drive('not_connected'))
    fireEvent.click(screen.getByRole('button', { name: 'Connect Google Drive' }))
    expect(await screen.findByText('Google Drive is unreachable.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Connect Google Drive' })).toBeEnabled()
    expect(assign).not.toHaveBeenCalled()
  })

  it('says Drive is not set up, without a Connect button', () => {
    serveBackups()
    openBackups(drive('not_configured'))
    expect(screen.getByText('Google Drive is not set up on this server.')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Connect Google Drive/ })).toBeNull()
  })
})

describe('Back up all now', () => {
  it.each(['not_connected', 'expired', 'not_configured'])('is disabled while Drive is %s', (s) => {
    serveBackups([okRow(1)])
    openBackups(drive(s))
    expect(allButton()).toBeDisabled()
  })

  it('posts and shows the batch at once', async () => {
    const stub = serveBackups([okRow(1)], {
      all: () => json(202, { id: 4, total: 6, skipped: 2 }),
    })
    openBackups(connected)
    expect(allButton()).toBeEnabled()
    fireEvent.click(allButton())
    expect(await screen.findByText('Back up all: 0 of 6 done')).toBeInTheDocument()
    const post = stub.mock.calls.find((c) => c[0] === '/api/backups/all') as unknown as [
      string,
      RequestInit,
    ]
    expect(post[1].method).toBe('POST')
    expect(JSON.parse(String(post[1].body))).toEqual({})
    expect(allButton()).toBeDisabled()
  })

  it('says why the batch was refused', async () => {
    serveBackups([okRow(1)], {
      all: () =>
        json(409, { error: 'batch_running', message: 'A Back up all is already running.' }),
    })
    openBackups(connected)
    fireEvent.click(allButton())
    const toast = await screen.findByRole('alert')
    expect(toast).toHaveTextContent("Couldn't start Back up all")
    expect(toast).toHaveTextContent('A Back up all is already running.')
    expect(allButton()).toBeEnabled()
  })

  it('follows the live batch, naming the backup running now', async () => {
    serveBackups([okRow(1)])
    openBackups(connected, {
      backups: [runningCrm],
      batch: batch({ done: 2, ok: 2 }),
    })
    expect(batchStatus()).toHaveTextContent('Back up all: 2 of 6 done · backing up crm · crm_db')
    expect(allButton()).toBeDisabled()
  })

  it('shows the result when the batch ends, with the skipped line, until dismissed', async () => {
    serveBackups([okRow(1)])
    const done = batch({
      done: 6,
      ok: 5,
      failed: 1,
      skipped: 2,
      finishedAt: '2026-10-31T10:05:00Z',
    })
    const first = openBackups(connected, { batch: done })
    const result = batchStatus()
    expect(result).toHaveTextContent('Back up all finished: 5 succeeded, 1 failed.')
    expect(result).toHaveTextContent('2 databases were skipped: not supported.')
    expect(allButton()).toBeEnabled()

    fireEvent.click(within(result).getByRole('button', { name: 'Dismiss' }))
    expect(screen.queryByTestId('backup-all')).toBeNull()

    // A reload keeps it dismissed for that batch; the next batch shows again.
    first.unmount()
    openBackups(connected, { batch: done })
    await screen.findByRole('table', { name: 'All backups' })
    expect(screen.queryByTestId('backup-all')).toBeNull()
    push(connected, { batch: { ...done, id: 5, skipped: 1 } })
    expect(batchStatus()).toHaveTextContent('1 database was skipped: not supported.')
  })

  it('works without localStorage', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('denied')
    })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('denied')
    })
    serveBackups([okRow(1)])
    openBackups(connected, { batch: batch({ done: 6, ok: 6, finishedAt: '2026-10-31T10:05:00Z' }) })
    const result = batchStatus()
    expect(result).toHaveTextContent('Back up all finished: 6 succeeded, 0 failed.')
    expect(result).not.toHaveTextContent('skipped')
    fireEvent.click(within(result).getByRole('button', { name: 'Dismiss' }))
    expect(screen.queryByTestId('backup-all')).toBeNull()
  })

  it('toasts the result in the tab that started it', async () => {
    serveBackups([okRow(1)], { all: () => json(202, { id: 4, total: 2, skipped: 0 }) })
    openBackups(connected)
    fireEvent.click(allButton())
    await screen.findByText('Back up all: 0 of 2 done')
    push(connected, { batch: batch({ total: 2, done: 1, ok: 1 }) })
    expect(screen.queryByTestId('toast-success')).toBeNull()
    push(connected, {
      batch: batch({ total: 2, done: 2, ok: 2, finishedAt: '2026-10-31T10:05:00Z' }),
    })
    expect(await screen.findByTestId('toast-success')).toHaveTextContent(
      'Back up all finished: 2 succeeded, 0 failed.',
    )
    // Only once.
    push(connected, {
      batch: batch({ total: 2, done: 2, ok: 2, finishedAt: '2026-10-31T10:05:00Z' }),
    })
    expect(screen.getAllByTestId('toast-success')).toHaveLength(1)
  })

  it('toasts a failure as an error', async () => {
    serveBackups([okRow(1)], { all: () => json(202, { id: 4, total: 2, skipped: 1 }) })
    openBackups(connected)
    fireEvent.click(allButton())
    await screen.findByText('Back up all: 0 of 2 done')
    push(connected, {
      batch: batch({
        total: 2,
        done: 2,
        ok: 1,
        failed: 1,
        skipped: 1,
        finishedAt: '2026-10-31T10:05:00Z',
      }),
    })
    const toast = await screen.findByRole('alert')
    expect(toast).toHaveTextContent('Back up all finished: 1 succeeded, 1 failed.')
    expect(toast).toHaveTextContent('1 database was skipped: not supported.')
  })

  it('does not toast a batch another tab started', async () => {
    serveBackups([okRow(1)])
    openBackups(connected, { batch: batch({ done: 1, ok: 1, total: 2 }) })
    push(connected, {
      batch: batch({ total: 2, done: 2, ok: 2, finishedAt: '2026-10-31T10:05:00Z' }),
    })
    await screen.findByText('Back up all finished: 2 succeeded, 0 failed.')
    expect(screen.queryByTestId('toast-success')).toBeNull()
  })
})

describe('summary and active backups', () => {
  it('reads the summary again when the live backups or batch change', async () => {
    const stub = serveBackups([okRow(1)])
    openBackups(connected)
    await screen.findByRole('region', { name: 'Drive usage' })
    const before = callsTo(stub, '/api/backups/summary').length
    expect(before).toBe(1)

    push(connected, { backups: [runningCrm] })
    await waitFor(() => expect(callsTo(stub, '/api/backups/summary')).toHaveLength(2))
    // Progress of the same backup does not re-read it.
    push(connected, { backups: [{ ...runningCrm, steps: uploading.slice() }] })
    push(connected, { batch: batch({ done: 1 }) })
    await waitFor(() => expect(callsTo(stub, '/api/backups/summary')).toHaveLength(3))
  })

  it('shows one card per active backup, running first, above the filters', async () => {
    serveBackups([okRow(1)])
    const waiting = makeBackup({
      id: 8,
      projectId: 2,
      projectName: 'crm',
      database: { ...makeBackup().database, name: 'crm_db' },
      queuePosition: 1,
      createdAt: new Date(2026, 9, 31, 10, 44).toISOString(),
    })
    const running = makeBackup({
      id: 7,
      state: 'running',
      queuePosition: 0,
      steps: uploading,
      startedBy: 'admin@company.vn',
      createdAt: new Date(2026, 9, 31, 10, 42).toISOString(),
    })
    openBackups(connected, { backups: [waiting, running] })

    const cards = screen.getAllByRole('region', { name: /^(Backing up|Waiting to back up)/ })
    expect(cards).toHaveLength(2)
    expect(cards[0]).toHaveAccessibleName('Backing up shop · shop')
    expect(cards[0]).toHaveTextContent('Triggered by admin@company.vn · 10:42')
    const steps = within(cards[0]).getAllByRole('listitem')
    expect(steps.map((s) => s.getAttribute('data-state'))).toEqual([
      'done',
      'done',
      'running',
      'waiting',
    ])
    expect(within(cards[0]).getByRole('link', { name: 'shop' })).toHaveAttribute(
      'href',
      '/projects/1/database',
    )
    expect(cards[1]).toHaveAccessibleName('Waiting to back up crm · crm_db')
    expect(cards[1]).toHaveTextContent('Waiting · 1 before it')

    const filters = await screen.findByRole('group', { name: 'Filter by status' })
    for (const c of cards) {
      expect(c.compareDocumentPosition(filters) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    }
    expect(screen.queryByText('No backup is running.')).toBeNull()
  })

  it('names a scheduled backup in its card', () => {
    serveBackups([okRow(1)])
    openBackups(connected, { backups: [{ ...runningCrm, startedBy: 'schedule' }] })
    expect(screen.getByRole('region', { name: /^Backing up crm/ })).toHaveTextContent(
      'Triggered by Schedule',
    )
  })
})

describe('empty state', () => {
  it('says no backups yet, with Back up all now', async () => {
    const stub = serveBackups([], { all: () => json(202, { id: 4, total: 3, skipped: 0 }) })
    openBackups(connected)
    expect(await screen.findByText('No backups yet')).toBeInTheDocument()
    expect(
      screen.getByText('Run the first one now or set up a daily schedule.'),
    ).toBeInTheDocument()
    expect(screen.queryByRole('group', { name: 'Filter by status' })).toBeNull()
    expect(screen.queryByRole('table')).toBeNull()
    expect(screen.queryByText(/arrives with the next backup flow/)).toBeNull()

    const empty = screen.getByText('No backups yet').closest('div') as HTMLElement
    fireEvent.click(within(empty).getByRole('button', { name: 'Back up all now' }))
    await screen.findByText('Back up all: 0 of 3 done')
    expect(callsTo(stub, '/api/backups/all')).toHaveLength(1)
  })

  it('disables its Back up all now without Drive', async () => {
    serveBackups([])
    openBackups(drive('not_connected'))
    await screen.findByText('No backups yet')
    for (const b of screen.getAllByRole('button', { name: 'Back up all now' })) {
      expect(b).toBeDisabled()
    }
  })

  it('is not shown while a backup is active', async () => {
    serveBackups([])
    openBackups(connected, { backups: [runningCrm] })
    await screen.findByRole('table', { name: 'All backups' })
    expect(screen.queryByText('No backups yet')).toBeNull()
  })

  it('says nothing matches when filters hide every backup', async () => {
    serveBackups([])
    openBackups(connected, {}, '/backups?result=failed')
    expect(await screen.findByText('No backups match these filters.')).toBeInTheDocument()
    expect(screen.queryByText('No backups yet')).toBeNull()
    expect(screen.getByRole('group', { name: 'Filter by status' })).toBeInTheDocument()
  })
})

describe('the table', () => {
  it('reads the first page once the page opens', async () => {
    const stub = serveBackups([okRow(1)])
    openBackups(connected, { projects: [makeProject()] })
    await screen.findByRole('table', { name: 'All backups' })
    expect(listCalls(stub)).toEqual(['/api/backups'])
  })
})
