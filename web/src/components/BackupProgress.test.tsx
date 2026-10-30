import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Backup, BackupStep } from '../api/types.gen'
import { useBackup } from '../lib/useBackup'
import { FakeEventSource } from '../test/fakeEventSource'
import { makeBackup, makeProject, pushState } from '../test/projects'
import { renderApp } from '../test/renderApp'
import { BackupSteps } from './BackupProgress'

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status })

const STEPS = ['dump', 'compress', 'upload', 'prune']
const steps = (states: string[], details: string[] = []): BackupStep[] =>
  STEPS.map((step, i) => ({ step, state: states[i], detail: details[i] ?? '' }))

const connected = {
  state: 'connected',
  email: 'me@example.com',
  folder: { id: 'f', name: 'server-backups' },
  reason: '',
}

function Harness({ id = 7 }: { id?: number }) {
  const b = useBackup()
  return (
    <>
      <button type="button" onClick={() => b.open(id)}>
        Open
      </button>
      {b.dialogs}
    </>
  )
}

/** setup opens backup 7's dialog with the given active backups in the live state. */
function setup(active: Backup[]) {
  renderApp('/', { routes: [{ path: '/', element: <Harness /> }] })
  pushState([makeProject()], true, { drive: connected, backups: active })
  fireEvent.click(screen.getByRole('button', { name: 'Open' }))
}

const live = (active: Backup[]) =>
  pushState([makeProject()], true, { drive: connected, backups: active })

function serve(routes: Record<string, () => Response>) {
  const stub = vi.fn(async (url: string, init?: RequestInit) => {
    const key = `${init?.method ?? 'GET'} ${url}`
    const answer = routes[key]
    return answer ? answer() : json(404, { error: 'not found' })
  })
  vi.stubGlobal('fetch', stub)
  return stub
}
const callsTo = (stub: ReturnType<typeof vi.fn>, url: string) =>
  stub.mock.calls.filter((c) => c[0] === url)

beforeEach(() => {
  FakeEventSource.instances = []
  vi.stubGlobal('EventSource', FakeEventSource)
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('BackupSteps', () => {
  it('shows each step state with its icon and detail', () => {
    render(
      <BackupSteps
        backup={makeBackup({
          state: 'running',
          queuePosition: 0,
          steps: steps(
            ['done', 'done', 'running', 'waiting'],
            ['pg_dump · 18s', 'gzip · 212 → 38 MB', '23.6 / 38 MB · 62%', ''],
          ),
        })}
      />,
    )
    const items = screen.getAllByRole('listitem')
    expect(items.map((li) => li.getAttribute('data-state'))).toEqual([
      'done',
      'done',
      'running',
      'waiting',
    ])
    expect(items[0]).toHaveTextContent('1. Dump')
    expect(items[0]).toHaveTextContent('pg_dump · 18s')
    expect(within(items[0]).getByTestId('step-done')).toBeInTheDocument()
    expect(items[1]).toHaveTextContent('gzip · 212 → 38 MB')
    expect(within(items[2]).getByTestId('step-running')).toBeInTheDocument()
    expect(items[2]).toHaveAttribute('aria-current', 'step')
    expect(items[2]).toHaveTextContent('3. Upload to Drive')
    expect(items[2]).toHaveTextContent('23.6 / 38 MB · 62%')
    expect(within(items[2]).getByTestId('step-bar')).toHaveStyle({ width: '62%' })
    expect(within(items[3]).getByTestId('step-waiting')).toBeInTheDocument()
    expect(items[3]).toHaveTextContent('4. Remove old copies')
  })

  it('shows a failed step with its error and hint, and the skipped ones', () => {
    render(
      <BackupSteps
        backup={makeBackup({
          state: 'failed',
          failedStep: 'dump',
          error: "Access denied for user 'crm'",
          hint: 'Check MYSQL_PASSWORD in the db service.',
          queuePosition: 0,
          steps: steps(
            ['failed', 'skipped', 'skipped', 'skipped'],
            ['', '', '', 'temp file deleted'],
          ),
        })}
      />,
    )
    const items = screen.getAllByRole('listitem')
    expect(within(items[0]).getByTestId('step-failed')).toBeInTheDocument()
    expect(items[0]).toHaveTextContent("Access denied for user 'crm'")
    expect(items[0]).toHaveTextContent('Check MYSQL_PASSWORD in the db service.')
    expect(items[1]).toHaveTextContent('Skipped')
    expect(items[3]).toHaveTextContent('Skipped · temp file deleted')
  })

  it('is one line in compact form', () => {
    render(
      <BackupSteps
        compact
        backup={makeBackup({
          state: 'running',
          queuePosition: 0,
          steps: steps(['done', 'done', 'running', 'waiting'], ['', '', '23.6 / 38 MB · 62%']),
        })}
      />,
    )
    expect(screen.queryByRole('list')).toBeNull()
    expect(screen.getByText('Uploading to Drive · 62%')).toBeInTheDocument()
  })
})

describe('the progress dialog', () => {
  it('shows a waiting backup with its queue position', () => {
    serve({})
    setup([makeBackup({ queuePosition: 2 })])
    const dialog = screen.getByRole('dialog', { name: 'Backup shop · shop' })
    expect(
      within(dialog).getByText('Waiting', { selector: '[data-testid="backup-pill"]' }),
    ).toBeInTheDocument()
    expect(dialog).toHaveTextContent('Waiting · 2 before it')
    expect(within(dialog).queryByRole('button', { name: 'Retry' })).toBeNull()
    expect(within(dialog).queryByRole('link', { name: 'Open in Drive' })).toBeNull()
  })

  it('follows the live state while running', () => {
    serve({})
    setup([
      makeBackup({
        state: 'running',
        queuePosition: 0,
        steps: steps(['running', 'waiting', 'waiting', 'waiting'], ['pg_dump · 2 s · 4 MB read']),
      }),
    ])
    const dialog = screen.getByRole('dialog', { name: 'Backup shop · shop' })
    expect(within(dialog).getByTestId('backup-pill')).toHaveTextContent('Running')
    expect(dialog).toHaveTextContent('pg_dump · 2 s · 4 MB read')
    live([
      makeBackup({
        state: 'running',
        queuePosition: 0,
        steps: steps(
          ['done', 'done', 'running', 'waiting'],
          ['pg_dump · 18s', 'gzip · 2 → 1 MB', '0.5 / 1.0 MB · 50%'],
        ),
      }),
    ])
    expect(dialog).toHaveTextContent('0.5 / 1.0 MB · 50%')
  })

  it('fetches the final state once when the backup leaves the active list', async () => {
    const stub = serve({
      'GET /api/backups/7': () =>
        json(
          200,
          makeBackup({
            state: 'ok',
            queuePosition: 0,
            driveLink: 'https://drive.google.com/file/d/abc/view',
            steps: steps(
              ['done', 'done', 'done', 'skipped'],
              ['', '', '', 'No retention set yet. All copies are kept.'],
            ),
          }),
        ),
    })
    setup([makeBackup({ state: 'running', queuePosition: 0 })])
    expect(callsTo(stub, '/api/backups/7')).toHaveLength(0)
    live([])
    const dialog = screen.getByRole('dialog', { name: 'Backup shop · shop' })
    await waitFor(() =>
      expect(within(dialog).getByTestId('backup-pill')).toHaveTextContent('Succeeded'),
    )
    expect(dialog).toHaveTextContent('No retention set yet. All copies are kept.')
    const link = within(dialog).getByRole('link', { name: 'Open in Drive' })
    expect(link).toHaveAttribute('href', 'https://drive.google.com/file/d/abc/view')
    expect(link).toHaveAttribute('target', '_blank')
    live([])
    live([])
    await act(async () => {})
    expect(callsTo(stub, '/api/backups/7')).toHaveLength(1)
  })

  it('fetches again when a fetched backup was still active and a later state lacks it', async () => {
    let answers = [
      makeBackup({ state: 'queued', queuePosition: 1 }),
      makeBackup({ state: 'ok', queuePosition: 0 }),
    ]
    const stub = serve({
      'GET /api/backups/7': () => {
        const [first, ...rest] = answers
        if (rest.length > 0) answers = rest
        return json(200, first)
      },
    })
    setup([])
    const dialog = screen.getByRole('dialog', { name: /Backup/ })
    await waitFor(() =>
      expect(within(dialog).getByTestId('backup-pill')).toHaveTextContent('Waiting'),
    )
    expect(callsTo(stub, '/api/backups/7')).toHaveLength(1)
    live([])
    await waitFor(() =>
      expect(within(dialog).getByTestId('backup-pill')).toHaveTextContent('Succeeded'),
    )
    expect(callsTo(stub, '/api/backups/7')).toHaveLength(2)
    // A finished backup is final: later states fetch nothing.
    live([])
    await act(async () => {})
    expect(callsTo(stub, '/api/backups/7')).toHaveLength(2)
  })

  it('says the final read failed while it still shows the last live value', async () => {
    serve({ 'GET /api/backups/7': () => json(503, { error: 'The panel is shutting down.' }) })
    setup([makeBackup({ state: 'running', queuePosition: 0 })])
    live([])
    const dialog = screen.getByRole('dialog', { name: 'Backup shop · shop' })
    expect(await within(dialog).findByText('The panel is shutting down.')).toBeInTheDocument()
    expect(within(dialog).getByTestId('backup-pill')).toHaveTextContent('Running')
  })

  it('fetches a backup that is not active when it opens', async () => {
    const stub = serve({
      'GET /api/backups/7': () =>
        json(
          200,
          makeBackup({
            state: 'interrupted',
            queuePosition: 0,
            error: 'The panel restarted during this backup',
            steps: steps(['failed', 'skipped', 'skipped', 'skipped']),
          }),
        ),
    })
    setup([])
    const dialog = screen.getByRole('dialog', { name: /Backup/ })
    await waitFor(() =>
      expect(within(dialog).getByTestId('backup-pill')).toHaveTextContent('Interrupted'),
    )
    expect(dialog).toHaveTextContent('The panel restarted during this backup')
    expect(callsTo(stub, '/api/backups/7')).toHaveLength(1)
  })

  it('shows a refusal with its words above the skipped steps', async () => {
    serve({
      'GET /api/backups/7': () =>
        json(
          200,
          makeBackup({
            state: 'refused',
            queuePosition: 0,
            error: 'Start db first',
            steps: steps(['skipped', 'skipped', 'skipped', 'skipped']),
          }),
        ),
    })
    setup([])
    const dialog = screen.getByRole('dialog', { name: /Backup/ })
    await waitFor(() =>
      expect(within(dialog).getByTestId('backup-pill')).toHaveTextContent('Refused'),
    )
    expect(dialog).toHaveTextContent('Start db first')
    expect(within(dialog).getByRole('button', { name: 'Retry' })).toBeInTheDocument()
  })

  it('retries a failed backup and follows the new one', async () => {
    const stub = serve({
      'GET /api/backups/7': () =>
        json(
          200,
          makeBackup({
            state: 'failed',
            failedStep: 'upload',
            error: 'Google Drive is unreachable.',
            queuePosition: 0,
            steps: steps(['done', 'done', 'failed', 'skipped']),
          }),
        ),
      'POST /api/backups/7/retry': () => json(202, { id: 8 }),
      'GET /api/backups/8': () => json(200, makeBackup({ id: 8, queuePosition: 3 })),
    })
    setup([])
    const dialog = screen.getByRole('dialog', { name: /Backup/ })
    const retry = await within(dialog).findByRole('button', { name: 'Retry' })
    fireEvent.click(retry)
    await waitFor(() =>
      expect(screen.getByRole('dialog', { name: 'Backup shop · shop' })).toHaveTextContent(
        'Waiting · 3 before it',
      ),
    )
    const [, init] = callsTo(stub, '/api/backups/7/retry')[0]
    expect((init as RequestInit).method).toBe('POST')
    expect((init as RequestInit).headers).toMatchObject({ 'Content-Type': 'application/json' })
  })

  it('shows a refused retry as a toast and keeps the dialog', async () => {
    serve({
      'GET /api/backups/7': () =>
        json(
          200,
          makeBackup({
            state: 'failed',
            failedStep: 'dump',
            error: 'x',
            queuePosition: 0,
            steps: steps(['failed', 'skipped', 'skipped', 'skipped']),
          }),
        ),
      'POST /api/backups/7/retry': () =>
        json(409, {
          error: 'not_retryable',
          message: 'Only a failed, refused or interrupted backup can be retried.',
        }),
    })
    setup([])
    fireEvent.click(await screen.findByRole('button', { name: 'Retry' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Only a failed, refused or interrupted backup can be retried.',
    )
    expect(screen.getByRole('dialog', { name: 'Backup shop · shop' })).toBeInTheDocument()
  })

  it('closes without cancelling anything', () => {
    const stub = serve({})
    setup([makeBackup({ state: 'running', queuePosition: 0 })])
    fireEvent.click(screen.getByRole('button', { name: 'Close' }))
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(
      stub.mock.calls.filter((c) => (c[1] as RequestInit | undefined)?.method === 'POST'),
    ).toHaveLength(0)
  })

  it('says when the backup cannot be read', async () => {
    serve({ 'GET /api/backups/7': () => json(404, { error: 'This backup does not exist.' }) })
    setup([])
    expect(await screen.findByText('This backup does not exist.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Close' })).toBeInTheDocument()
  })
})
