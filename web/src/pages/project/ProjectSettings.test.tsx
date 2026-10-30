import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Project, SettingsRequest } from '../../api/types.gen'
import { projectRoute } from '../../app/App'
import { FakeEventSource } from '../../test/fakeEventSource'
import { makeProject, pushState, renderProject } from '../../test/projects'
import { renderApp } from '../../test/renderApp'

const SETTINGS = '/api/projects/1/settings'

const shop = makeProject({
  publicDomain: 'api.shop.test',
  alertCpuPercent: 90,
  alertRamMb: null,
})

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status })

/** serve answers each POST with next(url, body); everything else is the setup's 401. */
function serve(next: (url: string, body: unknown) => Promise<Response> | Response) {
  const stub = vi.fn(async (url: string, init?: RequestInit) => {
    if (init?.method === 'POST') return next(url, JSON.parse(String(init.body)))
    return json(401, { error: 'unauthorized' })
  })
  vi.stubGlobal('fetch', stub)
  return stub
}

const posts = (stub: ReturnType<typeof vi.fn>) =>
  stub.mock.calls.filter((c) => (c[1] as RequestInit | undefined)?.method === 'POST')

/** saved answers a settings POST with project p updated by the request, as the server does. */
const saved = (p: Project) => (_url: string, body: unknown) =>
  json(200, { ...p, ...(body as SettingsRequest) })

function open(project: Project = shop, path = '/projects/1/settings') {
  const r = renderProject(path)
  pushState([project])
  return r
}

const field = (name: string) => screen.getByLabelText(name) as HTMLInputElement
const displayName = () => field('Display name')
const domain = () => field('Public domain')
const template = () =>
  screen.getByLabelText('Start/stop command (from template)') as HTMLSelectElement
const cpu = () => field('CPU alert threshold (%)')
const ram = () => field('RAM alert threshold (MB)')
const save = () => screen.getByRole('button', { name: 'Save changes' })
const toasts = () => screen.getByTestId('toast-region')
const type = (el: HTMLElement, value: string) => fireEvent.change(el, { target: { value } })

beforeEach(() => {
  FakeEventSource.instances = []
  vi.stubGlobal('EventSource', FakeEventSource)
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('settings tab', () => {
  it('shows the fields with stack read only', () => {
    open()
    expect(displayName()).toHaveValue('shop')
    expect(domain()).toHaveValue('api.shop.test')
    expect(domain()).toHaveAttribute('placeholder', 'app.example.com')

    const stack = field('Stack')
    expect(stack).toHaveValue('docker compose')
    expect(stack).toHaveAttribute('readonly')

    expect(template()).toHaveValue('stop')
    expect(
      within(template())
        .getAllByRole('option')
        .map((o) => o.textContent),
    ).toEqual(['docker compose up -d / stop', 'docker compose up -d / down'])
    expect(template()).toBeEnabled()

    expect(cpu()).toHaveValue(90)
    expect(ram()).toHaveValue(null)
    expect(cpu()).toHaveAttribute('placeholder', 'Server default')
    expect(ram()).toHaveAttribute('placeholder', 'Server default')
    expect(
      screen.getByText('Alerts are not sent yet. The alerts flow will use these.'),
    ).toBeInTheDocument()
    expect(screen.queryByText("The panel's own project always stops with stop.")).toBeNull()

    const card = screen.getByRole('region', { name: 'Stop tracking project' })
    expect(card).toHaveTextContent(
      'Removes shop from the tool. Containers, files and backups on Drive are not touched.',
    )
    expect(within(card).getByRole('button', { name: 'Stop tracking' })).toBeEnabled()
  })

  it('save is enabled only when dirty', () => {
    open()
    expect(save()).toBeDisabled()
    type(displayName(), 'shop-api')
    expect(save()).toBeEnabled()
    // Back to the saved value: nothing to save again.
    type(displayName(), 'shop')
    expect(save()).toBeDisabled()

    type(template(), 'down')
    expect(save()).toBeEnabled()
    type(template(), 'stop')
    type(ram(), '512')
    expect(save()).toBeEnabled()
    type(ram(), '')
    expect(save()).toBeDisabled()
  })

  it('saves and toasts', async () => {
    const stub = serve(saved(shop))
    open()
    type(displayName(), 'shop-api')
    type(domain(), 'shop.example.com')
    type(template(), 'down')
    type(cpu(), '')
    type(ram(), '2048')
    fireEvent.click(save())

    await waitFor(() => expect(posts(stub)).toHaveLength(1))
    const [url, init] = posts(stub)[0] as [string, RequestInit]
    expect(url).toBe(SETTINGS)
    expect((init.headers as Record<string, string>)['Content-Type']).toBe('application/json')
    expect(JSON.parse(String(init.body))).toEqual({
      displayName: 'shop-api',
      publicDomain: 'shop.example.com',
      stopTemplate: 'down',
      alertCpuPercent: null,
      alertRamMb: 2048,
    })

    expect(await screen.findByTestId('toast-success')).toHaveTextContent(
      'Saved settings for shop-api',
    )
    // The draft is now the saved values.
    expect(save()).toBeDisabled()
    expect(displayName()).toHaveValue('shop-api')
  })

  it('takes the values the server saved', async () => {
    // The server trims and lowercases; the form shows what it stored.
    serve((_url, body) =>
      json(200, { ...shop, ...(body as SettingsRequest), publicDomain: 'shop.example.com' }),
    )
    open()
    type(domain(), ' Shop.Example.com ')
    fireEvent.click(save())
    await screen.findByTestId('toast-success')
    expect(domain()).toHaveValue('shop.example.com')
    expect(save()).toBeDisabled()
  })

  it('shows a 422 under its field and focuses it', async () => {
    serve(() =>
      json(422, {
        error: 'Enter a host name such as app.example.com.',
        field: 'publicDomain',
        code: 'invalid',
      }),
    )
    open()
    type(domain(), 'http://bad')
    fireEvent.click(save())

    const msg = await screen.findByText('Enter a host name such as app.example.com.')
    expect(domain()).toHaveFocus()
    expect(domain()).toHaveAttribute('aria-invalid', 'true')
    expect(domain()).toHaveAccessibleDescription('Enter a host name such as app.example.com.')
    expect(msg.closest('[role="alert"]')).not.toBeNull()
    expect(within(toasts()).queryByRole('alert')).toBeNull()
    // The draft stays, so it can be fixed and saved again.
    expect(save()).toBeEnabled()

    // Editing the field clears its message.
    type(domain(), 'shop.example.com')
    expect(screen.queryByText('Enter a host name such as app.example.com.')).toBeNull()
    expect(domain()).not.toHaveAttribute('aria-invalid')
  })

  it('places a 422 on a threshold too', async () => {
    serve(() =>
      json(422, { error: 'Enter 1 to 100.', field: 'alertCpuPercent', code: 'out_of_range' }),
    )
    open()
    type(cpu(), '150')
    fireEvent.click(save())
    await screen.findByText('Enter 1 to 100.')
    expect(cpu()).toHaveFocus()
  })

  it('shows other errors in a red toast', async () => {
    serve(() => json(404, { error: 'project not found' }))
    open()
    type(displayName(), 'shop-api')
    fireEvent.click(save())
    const toast = await within(toasts()).findByRole('alert')
    expect(toast).toHaveTextContent("Couldn't save settings for shop")
    expect(toast).toHaveTextContent('project not found')
    expect(save()).toBeEnabled()
  })

  it('keeps the draft across a live update', () => {
    open()
    type(displayName(), 'shop-api')
    pushState([{ ...shop, publicDomain: 'new.shop.test', alertRamMb: 1024 }])
    expect(displayName()).toHaveValue('shop-api')
    expect(domain()).toHaveValue('api.shop.test')
    expect(ram()).toHaveValue(null)
    expect(save()).toBeEnabled()

    // A live update that matches the draft leaves nothing to save.
    pushState([{ ...shop, displayName: 'shop-api' }])
    expect(displayName()).toHaveValue('shop-api')
    expect(save()).toBeDisabled()
  })

  it('asks before leaving with unsaved changes (tab link and back button)', async () => {
    const { router } = renderApp(['/projects/1', '/projects/1/settings'], {
      routes: [projectRoute, { path: '*', element: <div>elsewhere</div> }],
    })
    pushState([shop])
    type(displayName(), 'shop-api')

    const tabs = screen.getByRole('tablist')
    fireEvent.click(within(tabs).getByRole('tab', { name: 'Overview' }))
    let dialog = screen.getByRole('dialog', { name: 'Discard unsaved changes?' })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Keep editing' }))
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(router.state.location.pathname).toBe('/projects/1/settings')
    expect(displayName()).toHaveValue('shop-api')

    // The browser's back button asks too.
    await act(() => router.navigate(-1))
    dialog = screen.getByRole('dialog', { name: 'Discard unsaved changes?' })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Keep editing' }))
    expect(router.state.location.pathname).toBe('/projects/1/settings')

    fireEvent.click(within(tabs).getByRole('tab', { name: 'Logs' }))
    dialog = screen.getByRole('dialog', { name: 'Discard unsaved changes?' })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Discard' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/projects/1/logs'))
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('asks before leaving with unsaved changes (sidebar link)', async () => {
    renderApp('/projects/1/settings')
    pushState([shop])
    type(ram(), '512')

    const nav = screen.getByRole('navigation', { name: 'Main navigation' })
    fireEvent.click(within(nav).getByRole('link', { name: 'Server' }))
    let dialog = screen.getByRole('dialog', { name: 'Discard unsaved changes?' })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Keep editing' }))
    expect(ram()).toHaveValue(512)

    fireEvent.click(within(nav).getByRole('link', { name: 'Server' }))
    dialog = screen.getByRole('dialog', { name: 'Discard unsaved changes?' })
    fireEvent.click(within(dialog).getByRole('button', { name: 'Discard' }))
    expect(await screen.findByRole('heading', { level: 1, name: 'Server' })).toBeInTheDocument()
  })

  it('leaves without asking when nothing changed', () => {
    const { router } = open()
    fireEvent.click(within(screen.getByRole('tablist')).getByRole('tab', { name: 'Overview' }))
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(router.state.location.pathname).toBe('/projects/1')
  })

  it('lets the browser ask before closing the tab with unsaved changes', () => {
    open()
    const unload = () => {
      const e = new Event('beforeunload', { cancelable: true })
      window.dispatchEvent(e)
      return e.defaultPrevented
    }
    expect(unload()).toBe(false)
    type(displayName(), 'shop-api')
    expect(unload()).toBe(true)
    type(displayName(), 'shop')
    expect(unload()).toBe(false)
  })

  it('self project template is disabled with the note', async () => {
    const self = makeProject({ self: true, actions: ['untrack'] })
    const stub = serve(saved(self))
    open(self)
    expect(template()).toBeDisabled()
    expect(template()).toHaveAccessibleDescription(
      "The panel's own project always stops with stop.",
    )
    // Its name and domain can still change.
    type(displayName(), 'panel')
    fireEvent.click(save())
    await waitFor(() => expect(posts(stub)).toHaveLength(1))
    expect(JSON.parse(String((posts(stub)[0] as [string, RequestInit])[1].body))).toMatchObject({
      displayName: 'panel',
      stopTemplate: 'stop',
    })
  })

  it('stop tracking asks then returns to the dashboard', async () => {
    const stub = serve(() => json(200, { removed: 1 }))
    // Record every node the page adds, to see that "not tracked" never shows.
    const added: string[] = []
    const observer = new MutationObserver((records) => {
      for (const r of records) for (const n of r.addedNodes) added.push(n.textContent ?? '')
    })
    observer.observe(document.body, { childList: true, subtree: true })

    open()
    // Unsaved changes do not hold back a project that left the panel.
    type(displayName(), 'shop-api')
    const card = screen.getByRole('region', { name: 'Stop tracking project' })
    fireEvent.click(within(card).getByRole('button', { name: 'Stop tracking' }))
    const dialog = screen.getByRole('dialog')
    expect(dialog).toHaveTextContent('Stop tracking shop?')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Stop tracking' }))

    await waitFor(() => expect(posts(stub)).toHaveLength(1))
    expect((posts(stub)[0] as [string])[0]).toBe('/api/projects/1/untrack')
    expect(await screen.findByTestId('toast-success')).toHaveTextContent('Stopped tracking shop')
    expect((await screen.findByTestId('where')).textContent).toBe('/')
    expect(screen.queryByRole('dialog')).toBeNull()

    for (const r of observer.takeRecords())
      for (const n of r.addedNodes) added.push(n.textContent ?? '')
    observer.disconnect()
    expect(added.join('\n')).not.toContain('This project is not tracked.')
  })

  it('not found project offers Remove from list', async () => {
    const stub = serve(() => json(200, { removed: 1 }))
    open(makeProject({ status: 'notfound', actions: ['remove'], missingSince: '' }))
    const card = screen.getByRole('region', { name: 'Stop tracking project' })
    expect(within(card).queryByRole('button', { name: 'Stop tracking' })).toBeNull()
    fireEvent.click(within(card).getByRole('button', { name: 'Remove from list' }))
    const dialog = screen.getByRole('dialog')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Remove from list' }))
    await waitFor(() => expect(posts(stub)).toHaveLength(1))
    expect(await screen.findByTestId('toast-success')).toHaveTextContent('Removed shop')
    expect((await screen.findByTestId('where')).textContent).toBe('/')
  })

  it('not found project can still save', async () => {
    const missing = makeProject({ status: 'notfound', actions: ['remove'] })
    const stub = serve(saved(missing))
    open(missing)
    type(displayName(), 'old-shop')
    fireEvent.click(save())
    await waitFor(() => expect(posts(stub)).toHaveLength(1))
    expect((posts(stub)[0] as [string])[0]).toBe(SETTINGS)
    expect(await screen.findByTestId('toast-success')).toHaveTextContent(
      'Saved settings for old-shop',
    )
  })

  it('stop tracking is disabled while a command runs', () => {
    open(makeProject({ status: 'starting', actions: [] }))
    const card = screen.getByRole('region', { name: 'Stop tracking project' })
    const button = within(card).getByRole('button', { name: 'Stop tracking' })
    expect(button).toBeDisabled()
    expect(button).toHaveAttribute('title', 'Locked while a command runs')
  })

  it('one-column form at 375 px', () => {
    open()
    // Columns of at least 300 px, each as wide as the form when it is narrower.
    expect(screen.getByTestId('settings-form')).toHaveClass(
      'grid-cols-[repeat(auto-fit,minmax(min(300px,100%),1fr))]',
    )
  })
})
