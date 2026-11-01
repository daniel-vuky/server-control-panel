import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { AlertChannel, AlertsConfig } from '../../api/types.gen'
import { FakeEventSource } from '../../test/fakeEventSource'
import { renderApp } from '../../test/renderApp'
import AlertsCard from './AlertsCard'

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

const bodies = (stub: ReturnType<typeof vi.fn>, path: string) =>
  stub.mock.calls
    .filter((c) => c[0] === path && (c[1] as RequestInit | undefined)?.method === 'POST')
    .map((c) => JSON.parse(String((c[1] as RequestInit).body)))

const NONE: AlertChannel = {
  available: true,
  configured: false,
  enabled: false,
  tokenHint: '',
  chatId: '',
  recipients: [],
  reason: '',
}

const config = (over: Partial<AlertsConfig> = {}): AlertsConfig => ({
  telegram: {
    ...NONE,
    configured: true,
    enabled: true,
    tokenHint: '••••••:AAH…',
    chatId: '-100••••4821',
  },
  email: { ...NONE, configured: true, recipients: ['admin@company.vn', 'dev@company.vn'] },
  thresholds: { cpuPercent: 90, ramPercent: 90, diskPercent: 85, sustainedMinutes: 5 },
  canSeal: true,
  ...over,
})

function renderCard() {
  return renderApp('/settings', {
    routes: [{ path: '/settings', element: <AlertsCard /> }],
  })
}

const card = () => screen.getByRole('region', { name: 'Alerts' })
const row = (name: string) => within(card()).getByRole('group', { name })
const dialog = () => screen.getByRole('dialog')
const cpu = () => screen.getByLabelText('CPU threshold') as HTMLInputElement
const saveThresholds = () => within(card()).getByRole('button', { name: 'Save changes' })

beforeEach(() => {
  FakeEventSource.instances = []
  vi.stubGlobal('EventSource', FakeEventSource)
})
afterEach(() => vi.unstubAllGlobals())

describe('Alerts card: channels', () => {
  it('shows the sub-line, the Telegram hint and masked chat, and the Email recipients', async () => {
    route({ 'GET /api/alerts': () => json(200, config()) })
    renderCard()
    expect(card()).toHaveTextContent(
      'Sent when a project enters Error, a domain fails to respond 3 times in a row, a scheduled backup fails, a threshold is exceeded for the set time, or the Tunnel disconnects.',
    )
    await waitFor(() => expect(row('Telegram')).toBeInTheDocument())
    expect(row('Telegram')).toHaveTextContent('bot ••••••:AAH… → chat -100••••4821')
    expect(row('Email')).toHaveTextContent('admin@company.vn, dev@company.vn')
    expect(within(row('Telegram')).getByRole('button', { name: 'Edit' })).toBeInTheDocument()
    expect(within(row('Telegram')).getByRole('button', { name: 'Send test' })).toBeInTheDocument()
  })

  it('sets Telegram up: posts the token and chat, then shows the new hint', async () => {
    const stub = route({
      'GET /api/alerts': () =>
        json(200, config({ telegram: NONE, email: { ...NONE, configured: false } })),
      'POST /api/alerts/telegram': () =>
        json(
          200,
          config({
            telegram: {
              ...NONE,
              configured: true,
              enabled: true,
              tokenHint: '••••••:BBB…',
              chatId: '-100••••1177',
            },
          }),
        ),
    })
    renderCard()
    const set = await within(await screen.findByRole('group', { name: 'Telegram' })).findByRole(
      'button',
      { name: 'Set up' },
    )
    expect(row('Telegram')).toHaveTextContent('Not set up')
    expect(within(row('Telegram')).queryByRole('switch')).toBeNull()
    fireEvent.click(set)

    const dlg = within(dialog())
    fireEvent.change(dlg.getByLabelText('Bot token'), {
      target: { value: '123456:BBBxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx' },
    })
    fireEvent.change(dlg.getByLabelText('Chat ID'), { target: { value: '-1001234561177' } })
    fireEvent.click(dlg.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(bodies(stub, '/api/alerts/telegram')).toEqual([
      { token: '123456:BBBxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx', chatId: '-1001234561177' },
    ])
    expect(row('Telegram')).toHaveTextContent('bot ••••••:BBB… → chat -100••••1177')
    expect(within(row('Telegram')).getByRole('switch')).toBeChecked()
  })

  it('edits with the token empty: posts token "" and says the token is kept', async () => {
    const stub = route({
      'GET /api/alerts': () => json(200, config()),
      'POST /api/alerts/telegram': () => json(200, config()),
    })
    renderCard()
    await waitFor(() => expect(row('Telegram')).toBeInTheDocument())
    fireEvent.click(within(row('Telegram')).getByRole('button', { name: 'Edit' }))

    const dlg = within(dialog())
    const token = dlg.getByLabelText('Bot token')
    expect(token).toHaveAttribute('type', 'password')
    expect(dlg.getByText('Leave empty to keep the current token')).toBeInTheDocument()
    fireEvent.change(dlg.getByLabelText('Chat ID'), { target: { value: '-1001234564821' } })
    fireEvent.click(dlg.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(bodies(stub, '/api/alerts/telegram')).toEqual([{ token: '', chatId: '-1001234564821' }])
  })

  it('edits with the chat empty: shows the masked chat and posts chatId ""', async () => {
    const stub = route({
      'GET /api/alerts': () => json(200, config()),
      'POST /api/alerts/telegram': () => json(200, config()),
    })
    renderCard()
    await waitFor(() => expect(row('Telegram')).toBeInTheDocument())
    fireEvent.click(within(row('Telegram')).getByRole('button', { name: 'Edit' }))

    const dlg = within(dialog())
    const chat = dlg.getByLabelText('Chat ID')
    expect(chat).toHaveValue('')
    expect(chat).toHaveAttribute('placeholder', '-100••••4821')
    expect(dlg.getByText('Leave empty to keep the current chat')).toBeInTheDocument()
    fireEvent.click(dlg.getByRole('button', { name: 'Save' }))

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(bodies(stub, '/api/alerts/telegram')).toEqual([{ token: '', chatId: '' }])
  })

  it('setting Telegram up shows the chat id example, not the keep hint', async () => {
    route({
      'GET /api/alerts': () =>
        json(200, config({ telegram: NONE, email: { ...NONE, configured: false } })),
    })
    renderCard()
    const set = await within(await screen.findByRole('group', { name: 'Telegram' })).findByRole(
      'button',
      { name: 'Set up' },
    )
    fireEvent.click(set)
    const dlg = within(dialog())
    expect(dlg.getByText('The numeric id, like -1001234567890.')).toBeInTheDocument()
    expect(dlg.queryByText('Leave empty to keep the current chat')).toBeNull()
    expect(dlg.getByLabelText('Chat ID')).not.toHaveAttribute('placeholder')
  })

  it('places a 422 under its field and keeps the dialog open', async () => {
    route({
      'GET /api/alerts': () => json(200, config()),
      'POST /api/alerts/telegram': () =>
        json(422, { error: 'Enter the numeric chat id.', field: 'chatId', code: 'invalid' }),
    })
    renderCard()
    await waitFor(() => expect(row('Telegram')).toBeInTheDocument())
    fireEvent.click(within(row('Telegram')).getByRole('button', { name: 'Edit' }))
    const dlg = within(dialog())
    fireEvent.change(dlg.getByLabelText('Chat ID'), { target: { value: '@mychannel' } })
    fireEvent.click(dlg.getByRole('button', { name: 'Save' }))

    const msg = await dlg.findByText('Enter the numeric chat id.')
    expect(msg.closest('[role="alert"]')).not.toBeNull()
    expect(dlg.getByLabelText('Chat ID')).toHaveAttribute('aria-invalid', 'true')
    expect(dialog()).toBeInTheDocument()
  })

  it('says to set PANEL_SECRET_KEY on a 409 no_secret_key', async () => {
    route({
      'GET /api/alerts': () =>
        json(200, config({ telegram: NONE, email: { ...NONE, configured: false } })),
      'POST /api/alerts/telegram': () =>
        json(409, { error: 'no_secret_key', message: 'Set PANEL_SECRET_KEY in panel.env first.' }),
    })
    renderCard()
    const set = await within(await screen.findByRole('group', { name: 'Telegram' })).findByRole(
      'button',
      { name: 'Set up' },
    )
    fireEvent.click(set)
    const dlg = within(dialog())
    fireEvent.change(dlg.getByLabelText('Bot token'), { target: { value: '1:abc' } })
    fireEvent.change(dlg.getByLabelText('Chat ID'), { target: { value: '-100123' } })
    fireEvent.click(dlg.getByRole('button', { name: 'Save' }))
    expect(await dlg.findByText('Set PANEL_SECRET_KEY in panel.env first.')).toBeInTheDocument()
  })

  it('edits the email recipients, one per line', async () => {
    const stub = route({
      'GET /api/alerts': () => json(200, config()),
      'POST /api/alerts/email': () =>
        json(200, config({ email: { ...NONE, configured: true, recipients: ['ops@company.vn'] } })),
    })
    renderCard()
    await waitFor(() => expect(row('Email')).toBeInTheDocument())
    fireEvent.click(within(row('Email')).getByRole('button', { name: 'Edit' }))
    const dlg = within(dialog())
    const box = dlg.getByLabelText('Recipients') as HTMLTextAreaElement
    expect(box.value).toBe('admin@company.vn\ndev@company.vn')
    fireEvent.change(box, { target: { value: 'ops@company.vn' } })
    fireEvent.click(dlg.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(bodies(stub, '/api/alerts/email')).toEqual([{ recipients: 'ops@company.vn' }])
    expect(row('Email')).toHaveTextContent('ops@company.vn')
  })

  it('shows the reason and no switch or Set up for an email that is not available', async () => {
    route({
      'GET /api/alerts': () =>
        json(
          200,
          config({
            email: {
              ...NONE,
              available: false,
              reason: 'Set SMTP_HOST and SMTP_FROM in panel.env, see docs/alerts.md',
            },
          }),
        ),
    })
    renderCard()
    await waitFor(() => expect(row('Email')).toBeInTheDocument())
    expect(row('Email')).toHaveTextContent(
      'Set SMTP_HOST and SMTP_FROM in panel.env, see docs/alerts.md',
    )
    expect(within(row('Email')).queryByRole('switch')).toBeNull()
    expect(within(row('Email')).queryByRole('button', { name: 'Set up' })).toBeNull()
    expect(within(row('Email')).queryByRole('button', { name: 'Send test' })).toBeNull()
  })

  it('switches a channel: role switch, aria-checked, posts enabled', async () => {
    const stub = route({
      'GET /api/alerts': () => json(200, config()),
      'POST /api/alerts/telegram/enabled': () =>
        json(200, config({ telegram: { ...config().telegram, enabled: false } })),
    })
    renderCard()
    await waitFor(() => expect(row('Telegram')).toBeInTheDocument())
    const sw = within(row('Telegram')).getByRole('switch', { name: 'Enable alerts via Telegram' })
    expect(sw).toHaveAttribute('aria-checked', 'true')
    fireEvent.click(sw)
    await waitFor(() => expect(sw).toHaveAttribute('aria-checked', 'false'))
    expect(bodies(stub, '/api/alerts/telegram/enabled')).toEqual([{ enabled: false }])
  })

  it('Send test shows a spinner, then Delivered under the row', async () => {
    let answer: (r: Response) => void = () => {}
    route({
      'GET /api/alerts': () => json(200, config()),
      'POST /api/alerts/telegram/test': () =>
        new Promise<Response>((resolve) => {
          answer = resolve
        }),
    })
    renderCard()
    await waitFor(() => expect(row('Telegram')).toBeInTheDocument())
    const send = within(row('Telegram')).getByRole('button', { name: 'Send test' })
    fireEvent.click(send)
    await waitFor(() => expect(send).toBeDisabled())
    expect(send).toHaveAttribute('aria-busy', 'true')
    answer(json(200, { ok: true, message: 'Delivered' }))
    expect(await within(row('Telegram')).findByText('Delivered')).toBeInTheDocument()
    expect(send).toBeEnabled()
  })

  it('Send test shows the error words when the delivery failed', async () => {
    route({
      'GET /api/alerts': () => json(200, config()),
      'POST /api/alerts/email/test': () =>
        json(200, { ok: false, message: 'Could not reach the mail server.' }),
    })
    renderCard()
    await waitFor(() => expect(row('Email')).toBeInTheDocument())
    fireEvent.click(within(row('Email')).getByRole('button', { name: 'Send test' }))
    const words = await within(row('Email')).findByText('Could not reach the mail server.')
    expect(words.closest('[role="alert"]')).not.toBeNull()
  })

  it('Send test shows the words of a 409', async () => {
    route({
      'GET /api/alerts': () => json(200, config()),
      'POST /api/alerts/telegram/test': () =>
        json(409, { error: 'not_configured', message: 'Set up this channel first.' }),
    })
    renderCard()
    await waitFor(() => expect(row('Telegram')).toBeInTheDocument())
    fireEvent.click(within(row('Telegram')).getByRole('button', { name: 'Send test' }))
    expect(
      await within(row('Telegram')).findByText('Set up this channel first.'),
    ).toBeInTheDocument()
  })
})

describe('Alerts card: thresholds', () => {
  it('loads the thresholds with their units; Save changes waits for a change', async () => {
    route({ 'GET /api/alerts': () => json(200, config()) })
    renderCard()
    await waitFor(() => expect(cpu()).toHaveValue(90))
    expect(screen.getByLabelText('RAM threshold')).toHaveValue(90)
    expect(screen.getByLabelText('Disk threshold')).toHaveValue(85)
    expect(screen.getByLabelText('Sustained for')).toHaveValue(5)
    expect(saveThresholds()).toBeDisabled()
    fireEvent.change(cpu(), { target: { value: '80' } })
    expect(saveThresholds()).toBeEnabled()
    fireEvent.change(cpu(), { target: { value: '90' } })
    expect(saveThresholds()).toBeDisabled()
  })

  it('refuses 0 under the field without posting', async () => {
    const stub = route({ 'GET /api/alerts': () => json(200, config()) })
    renderCard()
    await waitFor(() => expect(cpu()).toHaveValue(90))
    fireEvent.change(cpu(), { target: { value: '0' } })
    fireEvent.click(saveThresholds())
    expect(await screen.findByText('Enter a whole number from 1 to 100.')).toBeInTheDocument()
    expect(cpu()).toHaveAttribute('aria-invalid', 'true')
    expect(bodies(stub, '/api/alerts/thresholds')).toEqual([])
  })

  it('refuses a sustained time over 60 with the minutes words', async () => {
    const stub = route({ 'GET /api/alerts': () => json(200, config()) })
    renderCard()
    await waitFor(() => expect(cpu()).toHaveValue(90))
    fireEvent.change(screen.getByLabelText('Sustained for'), { target: { value: '61' } })
    fireEvent.click(saveThresholds())
    expect(await screen.findByText('Enter a whole number from 1 to 60.')).toBeInTheDocument()
    expect(bodies(stub, '/api/alerts/thresholds')).toEqual([])
  })

  it('saves a valid change and toasts', async () => {
    const stub = route({
      'GET /api/alerts': () => json(200, config()),
      'POST /api/alerts/thresholds': () =>
        json(
          200,
          config({
            thresholds: { cpuPercent: 80, ramPercent: 90, diskPercent: 85, sustainedMinutes: 5 },
          }),
        ),
    })
    renderCard()
    await waitFor(() => expect(cpu()).toHaveValue(90))
    fireEvent.change(cpu(), { target: { value: '80' } })
    fireEvent.click(saveThresholds())
    expect(await screen.findByText('Alert settings saved')).toBeInTheDocument()
    expect(bodies(stub, '/api/alerts/thresholds')).toEqual([
      { cpuPercent: 80, ramPercent: 90, diskPercent: 85, sustainedMinutes: 5 },
    ])
    expect(cpu()).toHaveValue(80)
    expect(saveThresholds()).toBeDisabled()
  })

  it('places a 422 from the server under its field', async () => {
    route({
      'GET /api/alerts': () => json(200, config()),
      'POST /api/alerts/thresholds': () =>
        json(422, {
          error: 'Enter a whole number from 1 to 100.',
          field: 'diskPercent',
          code: 'range',
        }),
    })
    renderCard()
    await waitFor(() => expect(cpu()).toHaveValue(90))
    fireEvent.change(screen.getByLabelText('Disk threshold'), { target: { value: '70' } })
    fireEvent.click(saveThresholds())
    expect(await screen.findByText('Enter a whole number from 1 to 100.')).toBeInTheDocument()
    expect(screen.getByLabelText('Disk threshold')).toHaveAttribute('aria-invalid', 'true')
  })
})

describe('Alerts card: loading', () => {
  it('shows a retryable error when the read fails', async () => {
    let n = 0
    route({
      'GET /api/alerts': () => (n++ === 0 ? json(500, { error: 'boom' }) : json(200, config())),
    })
    renderCard()
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Could not load alert settings.')
    fireEvent.click(within(alert).getByRole('button', { name: 'Retry' }))
    await waitFor(() => expect(cpu()).toHaveValue(90))
  })
})
