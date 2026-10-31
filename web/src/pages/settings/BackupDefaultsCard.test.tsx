import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { BackupSettings } from '../../api/types.gen'
import { FakeEventSource } from '../../test/fakeEventSource'
import { renderApp } from '../../test/renderApp'
import BackupDefaultsCard from './BackupDefaultsCard'

const json = (code: number, body: unknown) => new Response(JSON.stringify(body), { status: code })

const settings: BackupSettings = {
  defaults: { frequency: 'daily', time: '02:00', weekday: 0, keep: 14, compress: true },
  timeZone: { name: 'Asia/Ho_Chi_Minh', offset: '+07:00' },
}

type Handler = (init?: RequestInit) => Response | Promise<Response>

function route(table: Record<string, Handler>) {
  const stub = vi.fn(async (url: string, init?: RequestInit) => {
    const key = `${init?.method ?? 'GET'} ${url}`
    const h = table[key]
    return h ? h(init) : json(404, { error: `unrouted ${key}` })
  })
  vi.stubGlobal('fetch', stub)
  return stub
}

const posts = (stub: ReturnType<typeof vi.fn>, url: string) =>
  stub.mock.calls
    .filter((c) => c[0] === url && (c[1] as RequestInit | undefined)?.method === 'POST')
    .map((c) => JSON.parse(String((c[1] as RequestInit).body)))

const DEFAULTS = '/api/backup-settings/defaults'

function renderCard() {
  return renderApp('/settings', {
    routes: [{ path: '/settings', element: <BackupDefaultsCard /> }],
  })
}

const card = () => screen.getByRole('region', { name: 'Backup defaults' })
const save = () => within(card()).getByRole('button', { name: 'Save changes' })

beforeEach(() => {
  FakeEventSource.instances = []
  vi.stubGlobal('EventSource', FakeEventSource)
})
afterEach(() => vi.unstubAllGlobals())

describe('Backup defaults card', () => {
  it('loads the defaults into the form, with the zone and the sub-line', async () => {
    route({ 'GET /api/backup-settings': () => json(200, settings) })
    renderCard()
    expect(card()).toHaveTextContent(
      "Applies to databases found from now on. Each database can still be set on its project's Database & Backup tab.",
    )
    expect(await screen.findByRole('radio', { name: 'Daily' })).toBeChecked()
    expect(screen.getByRole('radio', { name: 'Off' })).not.toBeChecked()
    expect(screen.getByLabelText('Run time')).toHaveValue('02:00')
    expect(screen.getByLabelText('Copies to keep')).toHaveValue(14)
    expect(screen.getByRole('switch', { name: 'Gzip compression' })).toBeChecked()
    expect(card()).toHaveTextContent('Asia/Ho_Chi_Minh, UTC+07:00')
    // Daily has no day; nothing changed yet, so Save waits.
    expect(screen.queryByLabelText('Day')).toBeNull()
    expect(save()).toBeDisabled()
  })

  it('has no encryption toggle', async () => {
    route({ 'GET /api/backup-settings': () => json(200, settings) })
    renderCard()
    await screen.findByRole('radio', { name: 'Daily' })
    expect(card()).not.toHaveTextContent(/encrypt/i)
  })

  it('shows Day for Weekly, Monday first', async () => {
    route({ 'GET /api/backup-settings': () => json(200, settings) })
    renderCard()
    fireEvent.click(await screen.findByRole('radio', { name: 'Weekly' }))
    const day = screen.getByLabelText('Day')
    expect(
      within(day)
        .getAllByRole('option')
        .map((o) => o.textContent),
    ).toEqual(['Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday', 'Sunday'])
    expect(day).toHaveValue('0')
    fireEvent.click(screen.getByRole('radio', { name: 'Off' }))
    expect(screen.queryByLabelText('Day')).toBeNull()
  })

  it('Save posts the fields, toasts and waits for the next change', async () => {
    const stub = route({
      'GET /api/backup-settings': () => json(200, settings),
      [`POST ${DEFAULTS}`]: (init) => json(200, JSON.parse(String(init?.body))),
    })
    renderCard()
    fireEvent.click(await screen.findByRole('radio', { name: 'Weekly' }))
    fireEvent.change(screen.getByLabelText('Day'), { target: { value: '3' } })
    fireEvent.change(screen.getByLabelText('Run time'), { target: { value: '03:30' } })
    fireEvent.change(screen.getByLabelText('Copies to keep'), { target: { value: '7' } })
    fireEvent.click(screen.getByRole('switch', { name: 'Gzip compression' }))
    expect(save()).toBeEnabled()
    fireEvent.click(save())
    expect(await screen.findByText('Saved backup defaults')).toBeInTheDocument()
    expect(posts(stub, DEFAULTS)).toEqual([
      { frequency: 'weekly', time: '03:30', weekday: 3, keep: 7, compress: false },
    ])
    expect(save()).toBeDisabled()
    expect(screen.getByLabelText('Day')).toHaveValue('3')
  })

  it('shows a 422 under its field and focuses it; editing the field clears it', async () => {
    route({
      'GET /api/backup-settings': () => json(200, settings),
      [`POST ${DEFAULTS}`]: () =>
        json(422, { error: 'Enter a whole number from 1 to 365.', field: 'keep', code: 'invalid' }),
    })
    renderCard()
    const keep = await screen.findByLabelText('Copies to keep')
    fireEvent.change(keep, { target: { value: '400' } })
    fireEvent.click(save())
    const msg = await screen.findByText('Enter a whole number from 1 to 365.')
    expect(keep).toHaveAttribute('aria-invalid', 'true')
    expect(keep.getAttribute('aria-describedby')).toContain(msg.closest('p')?.id)
    expect(keep).toHaveFocus()
    fireEvent.change(keep, { target: { value: '30' } })
    expect(screen.queryByText('Enter a whole number from 1 to 365.')).toBeNull()
  })

  it('a failed save that is not a field error toasts the words', async () => {
    route({
      'GET /api/backup-settings': () => json(200, settings),
      [`POST ${DEFAULTS}`]: () => json(500, { error: 'internal error' }),
    })
    renderCard()
    fireEvent.click(await screen.findByRole('radio', { name: 'Off' }))
    fireEvent.click(save())
    expect(await screen.findByText("Couldn't save backup defaults")).toBeInTheDocument()
    expect(screen.getByText('internal error')).toBeInTheDocument()
    expect(save()).toBeEnabled()
  })

  it('a failed load offers Retry', async () => {
    let fail = true
    route({
      'GET /api/backup-settings': () => (fail ? json(500, { error: 'boom' }) : json(200, settings)),
    })
    renderCard()
    expect(await screen.findByText('Could not load backup defaults.')).toBeInTheDocument()
    fail = false
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    await waitFor(() => expect(screen.getByRole('radio', { name: 'Daily' })).toBeChecked())
  })
})
