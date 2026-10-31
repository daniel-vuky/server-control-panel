import { screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { FakeEventSource } from '../../test/fakeEventSource'
import {
  leaveChartsUnanswered,
  makeContainer,
  makeProject,
  pushState,
  renderProject,
} from '../../test/projects'

const MB = 1024 * 1024

// Sorted by service, as the server sends them.
const containers = [
  makeContainer('app', {
    image: 'shop-app:2.4.1',
    ports: ['3000→3000', '9229 (internal)'],
    cpuPercent: 9.84,
    memBytes: 412 * MB,
  }),
  makeContainer('cache', {
    name: 'cache',
    state: '',
    status: 'Not created',
    image: '',
  }),
  makeContainer('db', {
    state: 'restarting',
    status: 'Restarting',
    exitReason: 'Restarting (exit 1)',
    restartCount: 14,
  }),
  makeContainer('worker', {
    state: 'exited',
    status: 'Exited',
    exitReason: 'Exited (0)',
    restartCount: 1,
  }),
]

const broken = makeProject({ status: 'error', actions: ['start', 'restart'], containers })

beforeEach(() => {
  FakeEventSource.instances = []
  vi.stubGlobal('EventSource', FakeEventSource)
  leaveChartsUnanswered()
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('project overview', () => {
  it('failed components panel lists non-running containers with View logs link to ?container=<service>&focus=error', () => {
    renderProject('/projects/1')
    pushState([broken])
    const panel = screen.getByRole('region', { name: 'Failed components' })
    const items = within(panel).getAllByRole('listitem')
    expect(items).toHaveLength(3)

    const [cache, db, worker] = items
    expect(cache).toHaveTextContent('cache')
    expect(cache).toHaveTextContent('Not created')
    expect(db).toHaveTextContent('shop-db-1')
    expect(db).toHaveTextContent('Restarting (exit 1)')
    expect(db).toHaveTextContent('restarted 14 times')
    expect(worker).toHaveTextContent('Exited (0)')
    expect(worker).toHaveTextContent('restarted 1 time')

    expect(within(db).getByRole('link', { name: /View logs/ })).toHaveAttribute(
      'href',
      '/projects/1/logs?container=db&focus=error',
    )
    expect(within(worker).getByRole('link', { name: /View logs/ })).toHaveAttribute(
      'href',
      '/projects/1/logs?container=worker&focus=error',
    )
  })

  it('a component with no restarts says nothing about restarts', () => {
    renderProject('/projects/1')
    pushState([broken])
    const panel = screen.getByRole('region', { name: 'Failed components' })
    expect(within(panel).getAllByRole('listitem')[0]).not.toHaveTextContent('restarted')
  })

  it('shows the panel for Partial too, and not for Running', () => {
    renderProject('/projects/1')
    pushState([{ ...broken, status: 'partial' }])
    expect(screen.getByRole('region', { name: 'Failed components' })).toBeInTheDocument()

    pushState([{ ...broken, status: 'running', containers: [containers[0]] }])
    expect(screen.queryByRole('region', { name: 'Failed components' })).toBeNull()
  })

  it('containers table rows and R/T summary', () => {
    renderProject('/projects/1')
    pushState([broken])
    const card = screen.getByRole('region', { name: 'Containers' })
    expect(card).toHaveTextContent('docker compose · 1/4 running')

    const table = within(card).getByRole('table')
    expect(
      within(table)
        .getAllByRole('columnheader')
        .map((h) => h.textContent),
    ).toEqual(['Name', 'Status', 'Image', 'Port', 'CPU', 'RAM', 'Restart'])

    const rows = within(table).getAllByRole('row').slice(1)
    expect(rows).toHaveLength(4)
    const cells = (r: HTMLElement) => within(r).getAllByRole('cell')

    const app = cells(rows[0])
    expect(app[0]).toHaveTextContent('shop-app-1')
    expect(app[1]).toHaveTextContent('Running')
    expect(app[2]).toHaveTextContent('shop-app:2.4.1')
    expect(app[3]).toHaveTextContent('3000→3000, 9229 (internal)')
    expect(app[4]).toHaveTextContent('9.8%')
    expect(app[5]).toHaveTextContent('412 MB')
    expect(app[6]).toHaveTextContent('0')

    const db = cells(rows[2])
    expect(db[0]).toHaveTextContent('shop-db-1')
    // The exit reason sits under the name of a row that is not running.
    expect(db[0]).toHaveTextContent('Restarting (exit 1)')
    expect(db[1]).toHaveTextContent('Restarting')
    expect(db[3]).toHaveTextContent('—')
    expect(db[4]).toHaveTextContent('—')
    expect(db[5]).toHaveTextContent('—')
    expect(db[6]).toHaveTextContent('14')

    expect(cells(rows[1])[1]).toHaveTextContent('Not created')
    expect(cells(rows[1])[2]).toHaveTextContent('—')
  })

  it('colours a failing status red and a normal stop grey', () => {
    renderProject('/projects/1')
    pushState([
      {
        ...broken,
        containers: [
          ...containers,
          makeContainer('mail', { state: 'exited', status: 'Exited', exitReason: 'Exited (2)' }),
        ],
      },
    ])
    const tone = (name: string) =>
      screen.getByRole('row', { name: new RegExp(name) }).getAttribute('data-tone')
    expect(tone('shop-app-1')).toBe('ok')
    expect(tone('shop-db-1')).toBe('failed')
    expect(tone('shop-mail-1')).toBe('failed')
    expect(tone('shop-worker-1')).toBe('stopped')
    expect(tone('cache')).toBe('stopped')
  })

  it('docker down shows the containers message', () => {
    renderProject('/projects/1')
    pushState([makeProject({ status: 'unknown', actions: ['untrack'] })], false)
    const card = screen.getByRole('region', { name: 'Containers' })
    expect(card).toHaveTextContent("Docker is not responding, so containers can't be read.")
    expect(within(card).queryByRole('table')).toBeNull()
    expect(screen.queryByRole('region', { name: 'Failed components' })).toBeNull()
  })

  it('updates values in place on a live update', () => {
    renderProject('/projects/1')
    pushState([broken])
    const row = screen.getByRole('row', { name: /shop-app-1/ })
    pushState([
      {
        ...broken,
        containers: [{ ...containers[0], cpuPercent: 20 }, ...containers.slice(1)],
      },
    ])
    expect(screen.getByRole('row', { name: /shop-app-1/ })).toBe(row)
    expect(row).toHaveTextContent('20.0%')
  })
})
