import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { BackupSettings, Database, DatabaseSchedule, DriveStatus } from '../../api/types.gen'
import { projectRoute } from '../../app/App'
import { FakeEventSource } from '../../test/fakeEventSource'
import { makeDatabase, makeProject, pushState, renderProject } from '../../test/projects'
import { renderApp } from '../../test/renderApp'

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status })

const connected: DriveStatus = {
  state: 'connected',
  email: 'me@example.com',
  folder: { id: 'f', name: 'server-backups' },
  reason: '',
}
const allowed = { allowed: true, reason: '', service: '' }
const notSupported = { allowed: false, reason: 'not_supported', service: '' }
const HISTORY = '/api/projects/1/backups?limit=50'
const SETTINGS = '/api/backup-settings'
const SAVE = '/api/projects/1/backup-schedule'

const zone = { name: 'Asia/Ho_Chi_Minh', offset: '+07:00' }
const settings: BackupSettings = {
  defaults: { frequency: 'daily', time: '02:00', weekday: 0, keep: 14, compress: true },
  timeZone: zone,
}

// 02:00 on Sunday Nov 1 in Ho Chi Minh City is 19:00 on Saturday Oct 31 in UTC.
const NEXT = '2026-11-01T02:00:00+07:00'

const daily = (over: Partial<DatabaseSchedule> = {}): DatabaseSchedule => ({
  frequency: 'daily',
  time: '02:00',
  weekday: 0,
  keep: 14,
  compress: true,
  nextRunAt: NEXT,
  ...over,
})
const off = (over: Partial<DatabaseSchedule> = {}) =>
  daily({ frequency: 'off', nextRunAt: null, ...over })

const shopDb = makeDatabase({ name: 'shop', backup: allowed, schedule: daily() })
const legacyDb = makeDatabase({
  type: 'mysql',
  name: 'legacy',
  backup: allowed,
  schedule: daily({ frequency: 'weekly', weekday: 3, time: '04:30', keep: 7, compress: false }),
})
const cacheDb = makeDatabase({
  type: 'sqlite',
  name: 'cache',
  locationKind: 'file',
  location: './data/cache.db',
  backup: notSupported,
  schedule: off(),
})

type Handler = (init?: RequestInit) => Response | Promise<Response>

/** serve answers fetch by "METHOD url"; the settings and an empty history unless given. */
function serve(table: Record<string, Handler> = {}) {
  const all: Record<string, Handler> = {
    [`GET ${SETTINGS}`]: () => json(200, settings),
    [`GET ${HISTORY}`]: () => json(200, { backups: [], more: false, stored: {} }),
    ...table,
  }
  const stub = vi.fn(async (url: string, init?: RequestInit) => {
    const h = all[`${init?.method ?? 'GET'} ${url}`]
    return h ? h(init) : json(404, { error: 'not found' })
  })
  vi.stubGlobal('fetch', stub)
  return stub
}

const posts = (stub: ReturnType<typeof vi.fn>) =>
  stub.mock.calls
    .filter((c) => c[0] === SAVE && (c[1] as RequestInit | undefined)?.method === 'POST')
    .map((c) => JSON.parse(String((c[1] as RequestInit).body)))

const project = (dbs: Database[]) => makeProject({ databases: dbs })

function open(dbs: Database[]) {
  renderProject('/projects/1/database')
  pushState([project(dbs)], true, { drive: connected })
}

const card = () => screen.getByRole('region', { name: 'Backup schedule' })
const save = () => within(card()).getByRole('button', { name: 'Save' })
const radio = (name: string) => within(card()).getByRole('radio', { name })

beforeEach(() => {
  FakeEventSource.instances = []
  vi.stubGlobal('EventSource', FakeEventSource)
})
afterEach(() => vi.unstubAllGlobals())

describe('backup schedule card', () => {
  it('edits the one database without a Database select', async () => {
    serve()
    open([shopDb])
    expect(radio('Daily')).toBeChecked()
    expect(within(card()).getByLabelText('Run time')).toHaveValue('02:00')
    expect(within(card()).getByLabelText('Copies to keep')).toHaveValue(14)
    expect(within(card()).getByRole('switch', { name: 'Gzip compression' })).toBeChecked()
    expect(within(card()).queryByLabelText('Database')).toBeNull()
    // Nothing changed yet.
    expect(save()).toBeDisabled()
    await waitFor(() => expect(card()).toHaveTextContent('Asia/Ho_Chi_Minh, UTC+07:00'))
  })

  it('switches the form with the Database select when there are several', () => {
    serve()
    open([shopDb, legacyDb])
    const select = within(card()).getByLabelText('Database')
    expect(
      within(select)
        .getAllByRole('option')
        .map((o) => o.textContent),
    ).toEqual(['shop', 'legacy'])
    expect(radio('Daily')).toBeChecked()

    fireEvent.change(select, { target: { value: legacyDb.key } })
    expect(radio('Weekly')).toBeChecked()
    expect(within(card()).getByLabelText('Day')).toHaveValue('3')
    expect(within(card()).getByLabelText('Run time')).toHaveValue('04:30')
    expect(within(card()).getByLabelText('Copies to keep')).toHaveValue(7)
    expect(within(card()).getByRole('switch', { name: 'Gzip compression' })).not.toBeChecked()
    expect(save()).toBeDisabled()
  })

  it('asks before switching the database with unsaved changes', () => {
    serve()
    open([shopDb, legacyDb])
    fireEvent.click(radio('Off'))
    const select = within(card()).getByLabelText('Database')
    fireEvent.change(select, { target: { value: legacyDb.key } })
    let dialog = screen.getByRole('dialog', { name: 'Discard unsaved changes?' })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Keep editing' }))
    expect(select).toHaveValue(shopDb.key)
    expect(radio('Off')).toBeChecked()

    fireEvent.change(select, { target: { value: legacyDb.key } })
    dialog = screen.getByRole('dialog', { name: 'Discard unsaved changes?' })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Discard' }))
    expect(select).toHaveValue(legacyDb.key)
    expect(radio('Weekly')).toBeChecked()
    expect(save()).toBeDisabled()
  })

  it('shows the reason instead of the form for a database that cannot be backed up', () => {
    serve()
    open([cacheDb])
    expect(card()).toHaveTextContent('cache')
    expect(card()).toHaveTextContent('SQLite backups are not supported yet')
    expect(within(card()).queryByRole('radiogroup')).toBeNull()
    expect(within(card()).queryByRole('button', { name: 'Save' })).toBeNull()
  })

  it('lists only the databases that can be scheduled in the select', () => {
    serve()
    open([shopDb, cacheDb])
    // One supported database: no select, and the other one's reason.
    expect(within(card()).queryByLabelText('Database')).toBeNull()
    expect(radio('Daily')).toBeChecked()
    expect(card()).toHaveTextContent('SQLite backups are not supported yet')
  })

  it('says when there is no database to schedule', () => {
    serve()
    open([])
    expect(card()).toHaveTextContent('No databases detected in this project.')
    expect(within(card()).queryByRole('radiogroup')).toBeNull()
  })

  it('saves, toasts and shows the new next run', async () => {
    const answer = project([
      {
        ...shopDb,
        schedule: daily({
          frequency: 'weekly',
          weekday: 1,
          nextRunAt: '2026-11-02T02:00:00+07:00',
        }),
      },
    ])
    const stub = serve({ [`POST ${SAVE}`]: () => json(200, answer) })
    open([{ ...shopDb, schedule: off() }])
    expect(card()).toHaveTextContent('No scheduled backups. Back up now still works.')

    fireEvent.click(radio('Weekly'))
    expect(save()).toBeEnabled()
    fireEvent.change(within(card()).getByLabelText('Day'), { target: { value: '1' } })
    fireEvent.click(save())

    expect(await screen.findByTestId('toast-success')).toHaveTextContent('Schedule saved')
    expect(posts(stub)).toEqual([
      {
        database: shopDb.key,
        frequency: 'weekly',
        time: '02:00',
        weekday: 1,
        keep: 14,
        compress: true,
      },
    ])
    expect(card()).toHaveTextContent('Next run: Mon Nov 2, 02:00 (Asia/Ho_Chi_Minh, UTC+07:00)')
    expect(card()).not.toHaveTextContent('No scheduled backups.')
    expect(save()).toBeDisabled()
  })

  it('shows a field error under its field', async () => {
    serve({
      [`POST ${SAVE}`]: () =>
        json(422, { error: 'Keep 1 to 365 copies.', field: 'keep', code: 'out_of_range' }),
    })
    open([shopDb])
    const keep = within(card()).getByLabelText('Copies to keep')
    fireEvent.change(keep, { target: { value: '0' } })
    fireEvent.click(save())
    expect(await within(card()).findByText('Keep 1 to 365 copies.')).toBeInTheDocument()
    expect(keep).toHaveAttribute('aria-invalid', 'true')
    expect(screen.queryByTestId('toast-success')).toBeNull()
  })

  it('shows a database error at the Database select', async () => {
    serve({
      [`POST ${SAVE}`]: () =>
        json(422, {
          error: 'This database cannot be scheduled.',
          field: 'database',
          code: 'not_supported',
        }),
    })
    open([shopDb, legacyDb])
    fireEvent.click(radio('Off'))
    fireEvent.click(save())
    const select = within(card()).getByLabelText('Database')
    expect(
      await within(card()).findByText('This database cannot be scheduled.'),
    ).toBeInTheDocument()
    expect(select).toHaveAttribute('aria-invalid', 'true')
    expect(screen.queryByTestId('toast-success')).toBeNull()
  })

  it('shows a database error in the card when there is no select', async () => {
    serve({
      [`POST ${SAVE}`]: () =>
        json(422, {
          error: 'This database is no longer detected.',
          field: 'database',
          code: 'unknown',
        }),
    })
    open([shopDb])
    fireEvent.click(radio('Off'))
    fireEvent.click(save())
    expect(
      await within(card()).findByText('This database is no longer detected.'),
    ).toBeInTheDocument()
  })

  it("shows the next run in the server zone, not the browser's", async () => {
    serve()
    open([shopDb])
    await waitFor(() =>
      expect(card()).toHaveTextContent('Next run: Sun Nov 1, 02:00 (Asia/Ho_Chi_Minh, UTC+07:00)'),
    )
    // The same instant in the runner's zone (UTC) is Sat Oct 31, 19:00.
    expect(card()).not.toHaveTextContent('19:00')
  })

  it('reads the next run at the offset the server wrote while the zone is not known', async () => {
    serve({ [`GET ${SETTINGS}`]: () => json(503, { error: 'busy' }) })
    open([shopDb])
    await waitFor(() => expect(card()).toHaveTextContent('Next run: Sun Nov 1, 02:00 (UTC+07:00)'))
  })

  it('says Off runs nothing on its own', () => {
    serve()
    open([{ ...shopDb, schedule: off() }])
    expect(radio('Off')).toBeChecked()
    expect(card()).toHaveTextContent('No scheduled backups. Back up now still works.')
    expect(card()).not.toHaveTextContent('Next run')
  })

  it('says how many copies are kept and how many are on Drive', async () => {
    serve({
      [`GET ${HISTORY}`]: () =>
        json(200, { backups: [], more: false, stored: { [shopDb.key]: 9, [legacyDb.key]: 3 } }),
    })
    open([shopDb, legacyDb])
    await waitFor(() => expect(card()).toHaveTextContent('Keeping 14 · 9 on Drive'))
    fireEvent.change(within(card()).getByLabelText('Database'), {
      target: { value: legacyDb.key },
    })
    expect(card()).toHaveTextContent('Keeping 7 · 3 on Drive')
  })

  it('counts no copies on Drive for a database the history has none of', async () => {
    serve({
      [`GET ${HISTORY}`]: () => json(200, { backups: [], more: false, stored: {} }),
    })
    open([shopDb])
    await waitFor(() => expect(card()).toHaveTextContent('Keeping 14 · 0 on Drive'))
  })

  it('follows a live change while nothing is edited', () => {
    serve()
    open([shopDb])
    act(() => {})
    pushState([project([{ ...shopDb, schedule: daily({ time: '03:15' }) }])], true, {
      drive: connected,
    })
    expect(within(card()).getByLabelText('Run time')).toHaveValue('03:15')
    expect(save()).toBeDisabled()
  })

  it('asks before leaving with unsaved changes', async () => {
    serve()
    const { router } = renderApp(['/projects/1', '/projects/1/database'], {
      routes: [projectRoute, { path: '*', element: <div>elsewhere</div> }],
    })
    pushState([project([shopDb])], true, { drive: connected })
    fireEvent.click(radio('Off'))

    const tabs = screen.getByRole('tablist')
    fireEvent.click(within(tabs).getByRole('tab', { name: 'Overview' }))
    let dialog = screen.getByRole('dialog', { name: 'Discard unsaved changes?' })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Keep editing' }))
    expect(router.state.location.pathname).toBe('/projects/1/database')
    expect(radio('Off')).toBeChecked()

    fireEvent.click(within(tabs).getByRole('tab', { name: 'Logs' }))
    dialog = screen.getByRole('dialog', { name: 'Discard unsaved changes?' })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Discard' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/projects/1/logs'))
  })

  it('lets the browser ask before closing the tab with unsaved changes', () => {
    serve()
    open([shopDb])
    const unload = () => {
      const e = new Event('beforeunload', { cancelable: true })
      window.dispatchEvent(e)
      return e.defaultPrevented
    }
    expect(unload()).toBe(false)
    fireEvent.click(radio('Off'))
    expect(unload()).toBe(true)
  })
})
