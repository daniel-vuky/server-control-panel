import { fireEvent, render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { describe, expect, it } from 'vitest'
import type { UsageReport } from '../../api/types.gen'
import { GB, MB, makeUsage, makeUsageRow } from '../../test/server'
import UsageTable from './UsageTable'

const report = makeUsage([
  makeUsageRow(),
  makeUsageRow({
    projectId: 2,
    name: 'crm',
    status: 'partial',
    cpuPercent: 8,
    memBytes: 420 * MB,
    netBps: null,
    diskBytes: 1.2 * GB,
    diskPartial: true,
  }),
  makeUsageRow({
    projectId: 3,
    name: 'blog',
    status: 'error',
    cpuPercent: null,
    memBytes: 45 * MB,
  }),
  makeUsageRow({
    projectId: 4,
    name: 'landing',
    status: 'stopped',
    cpuPercent: 0,
    memBytes: 0,
    netBps: 0,
    diskBytes: 0.4 * GB,
  }),
  makeUsageRow({
    projectId: null,
    name: 'Server Control Panel',
    status: null,
    self: true,
    cpuPercent: 1,
    memBytes: 60 * MB,
    netBps: 0.1 * MB,
    diskBytes: null,
    diskAt: null,
  }),
])

function renderTable(r: UsageReport = report, dockerUp: boolean | null = true) {
  render(
    <MemoryRouter>
      <UsageTable report={r} dockerUp={dockerUp} />
    </MemoryRouter>,
  )
  return screen.getByRole('table')
}

const names = (table: HTMLElement) =>
  within(table)
    .getAllByRole('rowheader')
    .map((h) => within(h).getByTestId('app-name').textContent)

/** cells are a row's CPU, RAM, Disk and Network cells. */
const cells = (table: HTMLElement, name: string) => {
  const header = within(table)
    .getAllByRole('rowheader')
    .find((h) => within(h).getByTestId('app-name').textContent === name)
  const row = header?.closest('tr')
  if (!row) throw new Error(`no row ${name}`)
  return within(row).getAllByRole('cell')
}

const bar = (cell: HTMLElement) => cell.querySelector<HTMLElement>('[data-bar]')

describe('UsageTable', () => {
  it('has the title, the sub-line and the four measures', () => {
    const table = renderTable()
    expect(screen.getByRole('heading', { name: 'Usage by app' })).toBeInTheDocument()
    expect(
      screen.getByText('Bars = share of total server usage · click a column header to sort'),
    ).toBeInTheDocument()
    expect(
      within(table)
        .getAllByRole('columnheader')
        .map((h) => h.textContent),
    ).toEqual(['Project', 'CPU (of server)', 'RAM ↓', 'Disk', 'Network'])
  })

  it('sorts by RAM by default, marked with ↓ and aria-sort', () => {
    const table = renderTable()
    const ram = within(table).getByRole('columnheader', { name: /RAM/ })
    expect(ram).toHaveAttribute('aria-sort', 'descending')
    expect(ram).toHaveTextContent('↓')
    expect(within(table).getByRole('columnheader', { name: /CPU/ })).not.toHaveAttribute(
      'aria-sort',
    )
    expect(names(table)).toEqual(['shop', 'crm', 'Server Control Panel', 'blog', 'landing'])
  })

  it('clicking CPU re-sorts largest first with unknowns last', () => {
    const table = renderTable()
    fireEvent.click(within(table).getByRole('button', { name: /CPU/ }))
    expect(names(table)).toEqual(['shop', 'crm', 'Server Control Panel', 'landing', 'blog'])
    expect(within(table).getByRole('columnheader', { name: /CPU/ })).toHaveAttribute(
      'aria-sort',
      'descending',
    )
    expect(within(table).getByRole('columnheader', { name: /RAM/ })).not.toHaveAttribute(
      'aria-sort',
    )
    expect(within(table).getByRole('columnheader', { name: /CPU/ })).toHaveTextContent('↓')

    fireEvent.click(within(table).getByRole('button', { name: /Network/ }))
    // crm's network is unknown: last.
    expect(names(table)).toEqual(['shop', 'blog', 'Server Control Panel', 'landing', 'crm'])
  })

  it('shows each value with its share of the server total and a bar', () => {
    const table = renderTable()
    const [cpu, ram, disk, net] = cells(table, 'shop')
    expect(cpu).toHaveTextContent('12%')
    expect(cpu).toHaveTextContent('30%')
    expect(bar(cpu)).toHaveStyle({ width: '30%' })
    expect(ram).toHaveTextContent('640 MB')
    expect(ram).toHaveTextContent('10%')
    expect(bar(ram)).toHaveStyle({ width: '10%' })
    expect(disk).toHaveTextContent('4.0 GB')
    expect(disk).toHaveTextContent('4%')
    expect(net).toHaveTextContent('1.0 MB/s')
    expect(net).toHaveTextContent('25%')
  })

  it('caps a share at 100%', () => {
    const r = makeUsage([makeUsageRow({ cpuPercent: 80 })])
    const table = renderTable(r)
    const [cpu] = cells(table, 'shop')
    expect(cpu).toHaveTextContent('100%')
    expect(bar(cpu)).toHaveStyle({ width: '100%' })
  })

  it('a stopped project reads 0, an unknown value — with no bar', () => {
    const table = renderTable()
    const [cpu, ram, , net] = cells(table, 'landing')
    expect(cpu).toHaveTextContent('0%')
    expect(ram).toHaveTextContent('0 MB')
    expect(net).toHaveTextContent('0 B/s')

    const [blogCpu] = cells(table, 'blog')
    expect(blogCpu).toHaveTextContent('—')
    expect(bar(blogCpu)).toBeNull()
    const [, , , crmNet] = cells(table, 'crm')
    expect(crmNet).toHaveTextContent('—')
    expect(bar(crmNet)).toBeNull()
  })

  it('words the CPU share as a whole number, <1% below half a percent', () => {
    const r = makeUsage([
      makeUsageRow({ cpuPercent: 3.4 }),
      makeUsageRow({ projectId: 2, name: 'crm', cpuPercent: 0.2 }),
      makeUsageRow({ projectId: 3, name: 'blog', cpuPercent: 0.6 }),
    ])
    const table = renderTable(r)
    expect(cells(table, 'shop')[0]).toHaveTextContent(/^3%/)
    expect(cells(table, 'crm')[0]).toHaveTextContent(/^<1%/)
    expect(cells(table, 'blog')[0]).toHaveTextContent(/^1%/)
  })

  it('prefixes a partial disk value with ≥', () => {
    const table = renderTable()
    const [, , disk] = cells(table, 'crm')
    expect(disk).toHaveTextContent('≥ 1.2 GB')
    const [, , panelDisk] = cells(table, 'Server Control Panel')
    expect(panelDisk).toHaveTextContent('—')
  })

  it('links a project to its page and tags the panel row', () => {
    const table = renderTable()
    expect(within(table).getByRole('link', { name: 'shop' })).toHaveAttribute('href', '/projects/1')
    const panel = within(table)
      .getAllByRole('rowheader')
      .find((h) => h.textContent?.includes('Server Control Panel'))
    expect(panel).toBeDefined()
    if (!panel) return
    expect(within(panel).getByText('Panel')).toBeInTheDocument()
    expect(within(panel).queryByRole('link')).toBeNull()
    // The project's status is named, not colour alone.
    const shop = within(table).getByRole('link', { name: 'shop' }).closest('th')
    expect(shop).not.toBeNull()
    if (shop) expect(within(shop).getByRole('img', { name: 'Running' })).toBeInTheDocument()
  })

  it('tags a tracked panel project and links it', () => {
    const r = makeUsage([makeUsageRow({ projectId: 9, name: 'panel', self: true })])
    const table = renderTable(r)
    const link = within(table).getByRole('link', { name: 'panel' })
    expect(link).toHaveAttribute('href', '/projects/9')
    expect(within(table).getByText('Panel')).toBeInTheDocument()
  })

  it('shows dashes, not zeros, while Docker is down', () => {
    const table = renderTable(report, false)
    for (const name of ['shop', 'landing', 'Server Control Panel']) {
      const [cpu, ram, disk, net] = cells(table, name)
      expect(cpu).toHaveTextContent('—')
      expect(ram).toHaveTextContent('—')
      expect(net).toHaveTextContent('—')
      expect(bar(cpu)).toBeNull()
      expect(bar(ram)).toBeNull()
      expect(bar(net)).toBeNull()
      // The disk is read from the folders and the hourly volume read, not live.
      if (name !== 'Server Control Panel') expect(disk).not.toHaveTextContent('—')
    }
  })
})
