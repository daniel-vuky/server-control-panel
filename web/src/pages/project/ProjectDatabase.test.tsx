import { fireEvent, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { BackupStep, DriveStatus } from '../../api/types.gen'
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

const uploading: BackupStep[] = [
  { step: 'dump', state: 'done', detail: 'pg_dump · 18s' },
  { step: 'compress', state: 'done', detail: 'gzip · 212 → 38 MB' },
  { step: 'upload', state: 'running', detail: '23.6 / 38 MB · 62%' },
  { step: 'prune', state: 'waiting', detail: '' },
]

/** serve answers fetch by "METHOD url"; anything else is a 404. */
function serve(routes: Record<string, (init?: RequestInit) => Response>) {
  const stub = vi.fn(async (url: string, init?: RequestInit) => {
    const answer = routes[`${init?.method ?? 'GET'} ${url}`]
    return answer ? answer(init) : json(404, { error: 'not found' })
  })
  vi.stubGlobal('fetch', stub)
  return stub
}

beforeEach(() => {
  FakeEventSource.instances = []
  vi.stubGlobal('EventSource', FakeEventSource)
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('database and backup tab', () => {
  it('lists detected databases, each with Back up now', () => {
    serve({ [`GET ${HISTORY}`]: () => json(200, { backups: [], more: false }) })
    renderProject('/projects/1/database')
    pushState(
      [
        makeProject({
          // The server names the Drive sub-folder; the page never slugs the name.
          driveFolder: 'shop-from-server',
          databases: [
            makeDatabase({
              name: 'shop_production',
              sources: ['DATABASE_URL', 'POSTGRES_DB'],
              backup: allowed,
            }),
            makeDatabase({
              type: 'sqlite',
              name: 'cache',
              locationKind: 'file',
              location: './data/cache.db',
              backup: { allowed: false, reason: 'not_supported', service: '' },
            }),
            makeDatabase({
              type: 'mysql',
              name: 'legacy',
              locationKind: 'external',
              location: 'db.example.com',
              sources: ['MYSQL_HOST'],
              backup: { allowed: false, reason: 'not_supported', service: '' },
            }),
          ],
        }),
      ],
      true,
      { drive: connected },
    )
    const card = screen.getByRole('region', { name: 'Detected databases' })
    const items = within(card).getAllByRole('listitem')
    expect(items).toHaveLength(3)

    expect(items[0]).toHaveTextContent('shop_production')
    expect(items[0]).toHaveTextContent(
      'PostgreSQL · in container db · from DATABASE_URL, POSTGRES_DB',
    )
    expect(items[0]).toHaveTextContent('Password hidden')
    expect(within(items[0]).getByRole('button', { name: 'Back up now' })).toBeEnabled()

    expect(items[1]).toHaveTextContent('SQLite · file ./data/cache.db')
    // A SQLite file has no password to hide.
    expect(items[1]).not.toHaveTextContent('Password hidden')
    const sqlite = within(items[1]).getByRole('button', { name: 'Back up now' })
    expect(sqlite).toBeDisabled()
    expect(sqlite).toHaveAttribute('title', 'SQLite backups are not supported yet')

    expect(items[2]).toHaveTextContent('MySQL · external db.example.com · from MYSQL_HOST')
    expect(within(items[2]).getByRole('button', { name: 'Back up now' })).toHaveAttribute(
      'title',
      'Backups of databases outside a container are not supported yet',
    )

    // Where the copies go on Drive: the folder, then the project's sub-folder.
    expect(card).toHaveTextContent('Stored in Drive/server-backups/shop-from-server/')
  })

  it('says when no database was detected', () => {
    renderProject('/projects/1/database')
    pushState([makeProject()])
    const card = screen.getByRole('region', { name: 'Detected databases' })
    expect(card).toHaveTextContent('No databases detected in this project.')
    expect(within(card).queryByRole('listitem')).toBeNull()
    // Without a connected folder there is no Drive path to show.
    expect(card).not.toHaveTextContent('Stored in')
  })

  it('backs up the database whose button was pressed', async () => {
    const stub = serve({
      [`GET ${HISTORY}`]: () => json(200, { backups: [], more: false }),
      'POST /api/projects/1/backups': () => json(202, { id: 9 }),
      'GET /api/backups/9': () => json(200, makeBackup({ id: 9, database: legacyDb() })),
    })
    renderProject('/projects/1/database')
    const first = makeDatabase({ name: 'shop', backup: allowed })
    const second = makeDatabase({ type: 'mysql', name: 'legacy', backup: allowed })
    pushState([makeProject({ databases: [first, second] })], true, { drive: connected })

    const items = within(screen.getByRole('region', { name: 'Detected databases' })).getAllByRole(
      'listitem',
    )
    fireEvent.click(within(items[1]).getByRole('button', { name: 'Back up now' }))

    // No chooser: the database is known.
    await screen.findByRole('dialog', { name: 'Backup shop · legacy' })
    const posts = stub.mock.calls.filter((c) => c[0] === '/api/projects/1/backups')
    expect(posts).toHaveLength(1)
    expect(JSON.parse(String((posts[0][1] as RequestInit).body))).toEqual({
      database: second.key,
    })
  })

  it('shows a running backup inline, which opens its progress dialog', async () => {
    serve({ [`GET ${HISTORY}`]: () => json(200, { backups: [], more: false }) })
    renderProject('/projects/1/database')
    const db = makeDatabase({ name: 'shop', backup: allowed })
    const other = makeDatabase({ type: 'mysql', name: 'legacy', backup: allowed })
    pushState([makeProject({ databases: [db, other] })], true, {
      drive: connected,
      backups: [makeBackup({ state: 'running', queuePosition: 0, steps: uploading })],
    })

    const items = within(screen.getByRole('region', { name: 'Detected databases' })).getAllByRole(
      'listitem',
    )
    const inline = within(items[0]).getByRole('button', { name: 'Uploading to Drive · 62%' })
    // Only the database that is being backed up shows a row.
    expect(within(items[1]).queryByRole('button', { name: /Uploading/ })).toBeNull()

    fireEvent.click(inline)
    const dialog = await screen.findByRole('dialog', { name: 'Backup shop · shop' })
    expect(within(dialog).getByTestId('backup-pill')).toHaveTextContent('Running')
  })

  it('keeps the schedule as an empty state', () => {
    renderProject('/projects/1/database')
    pushState([makeProject()])
    const schedule = screen.getByRole('region', { name: 'Backup schedule' })
    expect(schedule).toHaveTextContent('Schedules arrive with the next backup flow.')
    expect(within(schedule).queryByRole('button')).toBeNull()
    expect(within(schedule).queryByRole('textbox')).toBeNull()
  })
})

function legacyDb() {
  return {
    type: 'mysql',
    name: 'legacy',
    locationKind: 'container',
    location: 'db',
    key: 'mysql:db:legacy',
  }
}
