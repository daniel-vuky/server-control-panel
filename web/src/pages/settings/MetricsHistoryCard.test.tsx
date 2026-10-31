import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { FakeEventSource } from '../../test/fakeEventSource'
import { renderApp } from '../../test/renderApp'
import MetricsHistoryCard from './MetricsHistoryCard'

const json = (code: number, body: unknown) => new Response(JSON.stringify(body), { status: code })

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

const SETTINGS = '/api/metrics-settings'

const posts = (stub: ReturnType<typeof vi.fn>) =>
  stub.mock.calls
    .filter((c) => c[0] === SETTINGS && (c[1] as RequestInit | undefined)?.method === 'POST')
    .map((c) => JSON.parse(String((c[1] as RequestInit).body)))

function renderCard() {
  return renderApp('/settings', {
    routes: [{ path: '/settings', element: <MetricsHistoryCard /> }],
  })
}

const card = () => screen.getByRole('region', { name: 'Metrics history' })
const save = () => within(card()).getByRole('button', { name: 'Save' })
const input = () => screen.getByLabelText('Keep metrics for') as HTMLInputElement

beforeEach(() => {
  FakeEventSource.instances = []
  vi.stubGlobal('EventSource', FakeEventSource)
})
afterEach(() => vi.unstubAllGlobals())

describe('Metrics history card', () => {
  it('loads the days kept, with the unit and the sub-line; Save waits for a change', async () => {
    route({ [`GET ${SETTINGS}`]: () => json(200, { retentionDays: 7 }) })
    renderCard()
    expect(card()).toHaveTextContent(
      'Older samples are deleted every hour. Charts of 24 hours and 7 days use 5-minute averages.',
    )
    await waitFor(() => expect(input()).toHaveValue(7))
    expect(card()).toHaveTextContent('days')
    expect(save()).toBeDisabled()

    fireEvent.change(input(), { target: { value: '14' } })
    expect(save()).toBeEnabled()
    fireEvent.change(input(), { target: { value: '7' } })
    expect(save()).toBeDisabled()
  })

  it('saves the new value and toasts', async () => {
    const stub = route({
      [`GET ${SETTINGS}`]: () => json(200, { retentionDays: 7 }),
      [`POST ${SETTINGS}`]: () => json(200, { retentionDays: 21 }),
    })
    renderCard()
    await waitFor(() => expect(input()).toHaveValue(7))
    fireEvent.change(input(), { target: { value: '21' } })
    fireEvent.click(save())
    expect(await screen.findByText('Metrics history saved')).toBeInTheDocument()
    expect(posts(stub)).toEqual([{ retentionDays: 21 }])
    expect(input()).toHaveValue(21)
    expect(save()).toBeDisabled()
  })

  it.each(['6', '31', '7.5', ''])('refuses %j under the field without posting', async (v) => {
    const stub = route({ [`GET ${SETTINGS}`]: () => json(200, { retentionDays: 7 }) })
    renderCard()
    await waitFor(() => expect(input()).toHaveValue(7))
    fireEvent.change(input(), { target: { value: v } })
    fireEvent.click(save())
    const alert = await within(card()).findByRole('alert')
    expect(alert).toHaveTextContent('Enter a whole number of days from 7 to 30.')
    expect(input()).toHaveAttribute('aria-invalid', 'true')
    expect(input()).toHaveFocus()
    expect(posts(stub)).toEqual([])

    // Editing clears the message.
    fireEvent.change(input(), { target: { value: '10' } })
    expect(within(card()).queryByRole('alert')).toBeNull()
  })

  it('shows a 422 under the field', async () => {
    route({
      [`GET ${SETTINGS}`]: () => json(200, { retentionDays: 7 }),
      [`POST ${SETTINGS}`]: () =>
        json(422, { error: 'Not today.', field: 'retentionDays', code: 'range' }),
    })
    renderCard()
    await waitFor(() => expect(input()).toHaveValue(7))
    fireEvent.change(input(), { target: { value: '20' } })
    fireEvent.click(save())
    expect(await within(card()).findByRole('alert')).toHaveTextContent('Not today.')
    expect(input()).toHaveAttribute('aria-invalid', 'true')
    await waitFor(() => expect(input()).toHaveFocus())
    expect(screen.queryByText('Metrics history saved')).toBeNull()
  })

  it('toasts any other failure', async () => {
    route({
      [`GET ${SETTINGS}`]: () => json(200, { retentionDays: 7 }),
      [`POST ${SETTINGS}`]: () => json(500, { error: 'Disk full.' }),
    })
    renderCard()
    await waitFor(() => expect(input()).toHaveValue(7))
    fireEvent.change(input(), { target: { value: '20' } })
    fireEvent.click(save())
    expect(await screen.findByText("Couldn't save metrics history")).toBeInTheDocument()
    expect(screen.getByText('Disk full.')).toBeInTheDocument()
  })

  it('shows a load error with a retry', async () => {
    let fail = true
    route({
      [`GET ${SETTINGS}`]: () =>
        fail ? json(500, { error: 'boom' }) : json(200, { retentionDays: 9 }),
    })
    renderCard()
    expect(await screen.findByText('Could not load metrics history.')).toBeInTheDocument()
    fail = false
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    await waitFor(() => expect(input()).toHaveValue(9))
  })
})
