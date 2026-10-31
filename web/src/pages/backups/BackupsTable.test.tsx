import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Backup, BackupStep } from '../../api/types.gen'
import { connected, json, listCalls, openBackups, push, serveBackups } from '../../test/backupsPage'
import { FakeEventSource } from '../../test/fakeEventSource'
import { makeBackup, makeDatabase, makeProject } from '../../test/projects'

const STEPS = ['dump', 'compress', 'upload', 'prune']
const steps = (states: string[]): BackupStep[] =>
  STEPS.map((step, i) => ({ step, state: states[i], detail: '' }))
const at = (d: number, h: number, m: number) => new Date(2026, 9, d, h, m).toISOString()
const MB = 1024 * 1024

const crmDb = { ...makeBackup().database, name: 'crm_db', key: 'postgresql:db:crm_db' }

const ok = (id: number, more: Partial<Backup> = {}) =>
  makeBackup({
    id,
    state: 'ok',
    queuePosition: 0,
    steps: steps(['done', 'done', 'done', 'done']),
    sizeBytes: 84 * MB,
    durationMs: 41_000,
    driveLink: `https://drive.google.com/file/d/f${id}/view`,
    createdAt: at(28, 2, 0),
    ...more,
  })

const projects = [
  makeProject({
    id: 1,
    displayName: 'shop',
    databases: [
      makeDatabase({
        name: 'shop',
        schedule: {
          frequency: 'daily',
          time: '02:00',
          weekday: 0,
          keep: 14,
          compress: true,
          nextRunAt: null,
        },
      }),
    ],
  }),
  makeProject({ id: 2, displayName: 'crm', path: '/srv/crm' }),
]

const table = () => screen.findByRole('table', { name: 'All backups' })
const chip = (name: string) =>
  within(screen.getByRole('group', { name: 'Filter by status' })).getByRole('button', { name })
const bodyRows = (t: HTMLElement) => within(t).getAllByRole('row').slice(1)

beforeEach(() => {
  FakeEventSource.instances = []
  vi.stubGlobal('EventSource', FakeEventSource)
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('BackupsTable rows', () => {
  it('has design 04 columns and every row state', async () => {
    const rows = [
      ok(10, { startedBy: 'schedule' }),
      makeBackup({
        id: 9,
        projectId: 2,
        projectName: 'crm',
        database: crmDb,
        state: 'failed',
        failedStep: 'dump',
        error: 'wrong DB password',
        durationMs: 62_000,
        queuePosition: 0,
        startedBy: 'schedule',
        createdAt: at(27, 2, 0),
      }),
      makeBackup({
        id: 8,
        state: 'refused',
        error: 'Google Drive is not connected.',
        queuePosition: 0,
        createdAt: at(26, 2, 0),
      }),
      makeBackup({ id: 7, state: 'interrupted', queuePosition: 0, createdAt: at(25, 2, 0) }),
      ok(6, { prunedAt: '2026-10-28T02:01:00Z', startedBy: 'admin@company.vn' }),
    ]
    serveBackups(rows)
    openBackups(connected, { projects })
    const t = await table()
    const heads = within(t)
      .getAllByRole('columnheader')
      .map((h) => h.textContent)
    expect(heads).toEqual([
      'Time',
      'Project',
      'Database',
      'Size',
      'Duration',
      'Status',
      'Triggered by',
      'File',
    ])
    const [r10, r9, r8, r7, r6] = bodyRows(t)

    expect(r10).toHaveTextContent('Succeeded')
    expect(r10).toHaveTextContent('84 MB')
    expect(r10).toHaveTextContent('41s')
    expect(r10).toHaveTextContent('Schedule')
    expect(within(r10).getByRole('link', { name: 'shop' })).toHaveAttribute(
      'href',
      '/projects/1/database',
    )
    expect(within(r10).getByRole('link', { name: 'Open in Drive' })).toHaveAttribute(
      'href',
      'https://drive.google.com/file/d/f10/view',
    )
    expect(within(r10).getByRole('link', { name: 'Download' })).toHaveAttribute(
      'href',
      '/api/backups/10/download',
    )

    expect(r9).toHaveTextContent('Failed at Dump · wrong DB password')
    expect(r9).toHaveTextContent('crm_db')
    expect(within(r9).getByRole('link', { name: 'crm' })).toHaveAttribute(
      'href',
      '/projects/2/database',
    )
    expect(within(r9).getByRole('button', { name: 'Retry' })).toBeInTheDocument()

    expect(r8).toHaveTextContent('Refused · Google Drive is not connected.')
    expect(r7).toHaveTextContent('Interrupted')
    expect(within(r7).getByRole('button', { name: 'Retry' })).toBeInTheDocument()

    expect(r6).toHaveTextContent('Removed (kept 14 newest)')
    expect(r6).toHaveTextContent('admin@company.vn')
    expect(within(r6).queryByRole('link', { name: 'Download' })).toBeNull()
  })

  it('follows design 04: fixed layout, 12 px gaps, plain links, text Retry', async () => {
    const failed = makeBackup({
      id: 9,
      state: 'failed',
      failedStep: 'dump',
      error: 'wrong DB password',
      queuePosition: 0,
      createdAt: at(27, 2, 0),
    })
    serveBackups([ok(10), failed])
    openBackups(connected, { projects })
    const t = await table()
    expect(t).toHaveClass('table-fixed')
    const cols = t.querySelectorAll('colgroup > col')
    expect([...cols].map((c) => (c as HTMLElement).style.width)).toEqual([
      '14%',
      '11%',
      '13%',
      '10%',
      '8%',
      '18%',
      '12%',
      '14%',
    ])
    const [r10, r9] = bodyRows(t)
    const cell = r10.querySelector('td') as HTMLElement
    expect(cell).toHaveClass('px-1.5', 'first:pl-4', 'last:pr-4')
    expect(cell).not.toHaveClass('px-3')
    for (const name of ['Open in Drive', 'Download']) {
      const link = within(r10).getByRole('link', { name })
      expect(link).toHaveClass('text-text-2', 'underline', 'hover:text-white')
      expect(link).not.toHaveClass('text-accent')
    }
    const retry = within(r9).getByRole('button', { name: 'Retry' })
    expect(retry).toHaveClass('h-7', 'px-2.5', 'rounded-md', 'text-[12.5px]')
    expect(retry.querySelector('svg')).toBeNull()
    // The dot takes the state's text colour: failed danger-text, succeeded success.
    const dot = (r: HTMLElement) => r.querySelector('span[aria-hidden="true"]')
    expect(dot(r9)).toHaveClass('bg-danger-text')
    expect(dot(r10)).toHaveClass('bg-success')
  })

  it('lets a pruned row wrap inside the fixed File column', async () => {
    serveBackups([ok(6, { prunedAt: '2026-10-28T02:01:00Z' })])
    openBackups(connected, { projects })
    const [row] = bodyRows(await table())
    const removed = within(row).getByText('Removed (kept 14 newest)')
    const file = removed.closest('td') as HTMLElement
    expect(file).not.toHaveClass('whitespace-nowrap')
    expect(removed.parentElement).toHaveClass('flex-wrap')
    expect(removed.parentElement).not.toHaveClass('whitespace-nowrap')
  })

  it('shows a skeleton card while the first page loads', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => new Promise(() => {})),
    )
    openBackups(connected, { projects })
    expect(await screen.findByRole('status', { name: 'Loading' })).toBeInTheDocument()
    expect(screen.queryByText('Loading backups…')).toBeNull()
  })

  it('shows an active backup with its live progress', async () => {
    const queued = makeBackup({ id: 11, state: 'queued', queuePosition: 0 })
    serveBackups([queued, ok(10)])
    openBackups(connected, { projects })
    await table()
    push(connected, {
      projects,
      backups: [
        {
          ...queued,
          state: 'running',
          steps: [
            { step: 'dump', state: 'done', detail: '' },
            { step: 'compress', state: 'done', detail: '' },
            { step: 'upload', state: 'running', detail: '1 / 2 MB · 50%' },
            { step: 'prune', state: 'waiting', detail: '' },
          ],
        },
      ],
    })
    const [r11] = bodyRows(await table())
    expect(r11).toHaveTextContent('Uploading to Drive · 50%')
  })

  it('reads the newest page again when an active backup ends', async () => {
    const running = makeBackup({ id: 11, state: 'running', queuePosition: 0 })
    let answer = [running, ok(10)]
    const stub = serveBackups([], {
      list: () => json(200, { backups: answer, more: false }),
    })
    openBackups(connected, { projects, backups: [running] })
    await table()
    answer = [ok(11), ok(10)]
    push(connected, { projects, backups: [] })
    await waitFor(() => expect(listCalls(stub).length).toBeGreaterThanOrEqual(2))
    await waitFor(async () => expect(bodyRows(await table())[0]).toHaveTextContent('Succeeded'))
  })

  it('opens a row progress dialog from its status', async () => {
    serveBackups([ok(10)], {
      other: (url) => (url === '/api/backups/10' ? json(200, ok(10)) : undefined),
    })
    openBackups(connected, { projects })
    const [r10] = bodyRows(await table())
    fireEvent.click(within(r10).getByRole('button', { name: 'Succeeded' }))
    expect(await screen.findByRole('dialog')).toBeInTheDocument()
  })
})

describe('BackupsTable filters', () => {
  it('reads the filters from the URL and sends them', async () => {
    const stub = serveBackups([ok(10)])
    openBackups(
      connected,
      { projects },
      '/backups?project=2&result=failed&from=2026-10-21&to=2026-10-28',
    )
    await table()
    expect(listCalls(stub)).toEqual([
      '/api/backups?project=2&result=failed&from=2026-10-21&to=2026-10-28',
    ])
    expect(screen.getByLabelText('Project')).toHaveValue('2')
    expect(chip('Failed')).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByLabelText('From')).toHaveValue('2026-10-21')
    expect(screen.getByLabelText('To')).toHaveValue('2026-10-28')
  })

  it('ignores filter values that are not valid', async () => {
    const stub = serveBackups([ok(10)])
    openBackups(connected, { projects }, '/backups?project=x&result=maybe&from=yesterday')
    await table()
    expect(listCalls(stub)).toEqual(['/api/backups'])
    expect(chip('All')).toHaveAttribute('aria-pressed', 'true')
  })

  it('writes a changed filter into the URL and reads again', async () => {
    const stub = serveBackups([ok(10)])
    const { router } = openBackups(connected, { projects })
    await table()

    fireEvent.click(chip('Succeeded'))
    await waitFor(() => expect(listCalls(stub).at(-1)).toBe('/api/backups?result=ok'))
    expect(router.state.location.search).toBe('?result=ok')

    fireEvent.change(screen.getByLabelText('Project'), { target: { value: '1' } })
    await waitFor(() => expect(listCalls(stub).at(-1)).toBe('/api/backups?project=1&result=ok'))

    fireEvent.change(screen.getByLabelText('From'), { target: { value: '2026-10-21' } })
    await waitFor(() =>
      expect(listCalls(stub).at(-1)).toBe('/api/backups?project=1&result=ok&from=2026-10-21'),
    )
    fireEvent.change(screen.getByLabelText('To'), { target: { value: '2026-10-28' } })
    await waitFor(() =>
      expect(listCalls(stub).at(-1)).toBe(
        '/api/backups?project=1&result=ok&from=2026-10-21&to=2026-10-28',
      ),
    )
    expect(new URLSearchParams(router.state.location.search).get('to')).toBe('2026-10-28')

    fireEvent.click(chip('All'))
    fireEvent.change(screen.getByLabelText('Project'), { target: { value: '' } })
    fireEvent.change(screen.getByLabelText('From'), { target: { value: '' } })
    fireEvent.change(screen.getByLabelText('To'), { target: { value: '' } })
    await waitFor(() => expect(listCalls(stub).at(-1)).toBe('/api/backups'))
    expect(router.state.location.search).toBe('')
  })

  it('drops a page that answers after the filters changed', async () => {
    const pending: ((r: Response) => void)[] = []
    const stub = serveBackups([], {
      list: (url) => {
        if (url === '/api/backups') return json(200, { backups: [ok(10)], more: false })
        return undefined as unknown as Response
      },
    })
    const inner = stub.getMockImplementation() as (u: string, i?: RequestInit) => Promise<Response>
    stub.mockImplementation(async (url: string, init?: RequestInit) => {
      if (url.startsWith('/api/backups?result=')) {
        return new Promise<Response>((resolve) => pending.push(resolve))
      }
      return inner(url, init)
    })
    openBackups(connected, { projects })
    await table()

    fireEvent.click(chip('Failed'))
    await waitFor(() => expect(pending).toHaveLength(1))
    fireEvent.click(chip('Succeeded'))
    await waitFor(() => expect(pending).toHaveLength(2))

    const failedRow = makeBackup({ id: 20, state: 'failed', failedStep: 'dump', queuePosition: 0 })
    // The Succeeded page answers first, then the stale Failed one.
    pending[1](json(200, { backups: [ok(21)], more: false }))
    const t = await table()
    await waitFor(() => expect(bodyRows(t).map((r) => r.getAttribute('data-id'))).toEqual(['21']))
    pending[0](json(200, { backups: [failedRow], more: false }))
    await new Promise((r) => setTimeout(r, 20))
    expect(bodyRows(await table()).map((r) => r.getAttribute('data-id'))).toEqual(['21'])
    expect(listCalls(stub)).toEqual([
      '/api/backups',
      '/api/backups?result=failed',
      '/api/backups?result=ok',
    ])
  })

  it('names the server zone the dates are read in', async () => {
    serveBackups([ok(10)])
    openBackups(connected, { projects })
    await table()
    expect(await screen.findByText('Dates in Asia/Ho_Chi_Minh, UTC+07:00')).toBeInTheDocument()
  })

  it('lists the tracked projects in the Project filter', async () => {
    serveBackups([ok(10)])
    openBackups(connected, { projects })
    await table()
    const options = within(screen.getByLabelText('Project'))
      .getAllByRole('option')
      .map((o) => o.textContent)
    expect(options).toEqual(['All', 'crm', 'shop'])
  })

  it('says why a filter was refused', async () => {
    serveBackups([], {
      list: () => json(400, { error: 'To is before From.', field: 'to' }),
    })
    openBackups(connected, { projects }, '/backups?from=2026-10-28&to=2026-10-21')
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Could not load the backups.')
    expect(alert).toHaveTextContent('To is before From.')
  })
})

describe('Show older', () => {
  it('appends the next page with before and the filters', async () => {
    const stub = serveBackups([], {
      list: (url) =>
        url.includes('before=')
          ? json(200, { backups: [ok(8), ok(7)], more: false })
          : json(200, { backups: [ok(10), ok(9)], more: true }),
    })
    openBackups(connected, { projects }, '/backups?result=ok')
    const t = await table()
    expect(bodyRows(t)).toHaveLength(2)
    fireEvent.click(screen.getByRole('button', { name: 'Show older' }))
    await waitFor(() => expect(bodyRows(t)).toHaveLength(4))
    expect(listCalls(stub).at(-1)).toBe('/api/backups?result=ok&before=9')
    expect(bodyRows(t).map((r) => r.getAttribute('data-id'))).toEqual(['10', '9', '8', '7'])
    expect(screen.queryByRole('button', { name: 'Show older' })).toBeNull()
  })

  it('keeps the older pages when the newest page is read again', async () => {
    const running = makeBackup({ id: 11, state: 'running', queuePosition: 0 })
    let newest = [running, ok(10)]
    serveBackups([], {
      list: (url) =>
        url.includes('before=')
          ? json(200, { backups: [ok(9)], more: false })
          : json(200, { backups: newest, more: true }),
    })
    openBackups(connected, { projects, backups: [running] })
    const t = await table()
    fireEvent.click(screen.getByRole('button', { name: 'Show older' }))
    await waitFor(() => expect(bodyRows(t)).toHaveLength(3))
    newest = [ok(11), ok(10)]
    push(connected, { projects, backups: [] })
    await waitFor(() => expect(bodyRows(t)[0]).toHaveTextContent('Succeeded'))
    expect(bodyRows(t).map((r) => r.getAttribute('data-id'))).toEqual(['11', '10', '9'])
  })
})
