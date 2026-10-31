import { fireEvent, render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { describe, expect, it, vi } from 'vitest'
import type { Project } from '../../api/types.gen'
import { formatLastScan } from '../../lib/time'
import { makeContainer, makeProject } from '../../test/projects'
import ProjectCard, { type ProjectItem } from './ProjectCard'

const MB = 1024 * 1024

function setup(project: Partial<Project> = {}, extra: Partial<ProjectItem> = {}) {
  const item: ProjectItem = {
    project: makeProject({ id: 7, ...project }),
    latest: { text: '02:00 today', tone: 'ok' },
    dockerDown: false,
    onStart: vi.fn(),
    onStop: vi.fn(),
    onRestart: vi.fn(),
    onUntrack: vi.fn(),
    backup: { enabled: true, reason: '', onRun: vi.fn() },
    ...extra,
  }
  render(
    <MemoryRouter>
      <ProjectCard {...item} />
    </MemoryRouter>,
  )
  return { item, card: screen.getByRole('article', { name: item.project.displayName }) }
}

describe('ProjectCard', () => {
  it('is an article named by the project, its name a link to the detail page', () => {
    const { card } = setup()
    expect(within(card).getByRole('link', { name: 'shop' })).toHaveAttribute('href', '/projects/7')
    expect(within(card).getByText('Running')).toBeInTheDocument()
    expect(within(card).queryByText('Monitor only')).toBeNull()
  })

  it('tags the panel itself as Monitor only, outside its name', () => {
    const { card } = setup({ displayName: 'panel', self: true, actions: [] })
    expect(within(card).getByText('Monitor only')).toBeInTheDocument()
    expect(screen.getByRole('article', { name: 'panel' })).toBe(card)
  })

  it('shows the stack chip from the containers', () => {
    const { card } = setup({
      containers: [makeContainer('web'), makeContainer('db', { state: 'exited' })],
    })
    expect(within(card).getByTestId('stack-chip')).toHaveTextContent('Compose · 1/2 containers')
  })

  it('links the public domain in a new tab, with no opener and no referrer', () => {
    const { card } = setup({ publicDomain: 'shop.vn' })
    const link = within(card).getByRole('link', { name: 'shop.vn' })
    expect(link).toHaveAttribute('href', 'https://shop.vn')
    expect(link).toHaveAttribute('target', '_blank')
    expect(link.getAttribute('rel')?.split(' ')).toEqual(
      expect.arrayContaining(['noopener', 'noreferrer']),
    )
  })

  it('says No public domain, not as a link, without one', () => {
    const { card } = setup()
    const words = within(card).getByText('No public domain')
    expect(words.closest('a')).toBeNull()
    expect(within(card).getAllByRole('link')).toHaveLength(1)
  })

  it('puts the folder-missing words in the domain place for a Not found project', () => {
    const missingSince = '2026-10-30T09:42:00Z'
    const { card } = setup({
      status: 'notfound',
      actions: ['remove'],
      missingSince,
      publicDomain: 'shop.vn',
    })
    expect(within(card).getByText('Not found')).toBeInTheDocument()
    expect(
      within(card).getByText(
        `Folder missing since the ${formatLastScan(missingSince)} scan. Not removed automatically.`,
      ),
    ).toBeInTheDocument()
    expect(within(card).queryByRole('link', { name: 'shop.vn' })).toBeNull()
    const sw = within(card).getByRole('switch')
    expect(sw).toBeDisabled()
    expect(sw).toHaveAttribute('title', 'Not allowed while Not found')
  })

  it('shows CPU and RAM with bars, against 100 % and the 4 GB range', () => {
    const { card } = setup({ cpuPercent: 12.4, memBytes: 640 * MB })
    expect(within(card).getByText('12%')).toBeInTheDocument()
    expect(within(card).getByText('640 MB')).toBeInTheDocument()
    expect(within(card).getByTestId('cpu-bar')).toHaveStyle({ width: '12.4%' })
    // 640 MB of the 4 GB range is 15.625 %.
    expect(within(card).getByTestId('ram-bar')).toHaveStyle({ width: '15.625%' })
  })

  it('caps the CPU bar at 100 %', () => {
    const { card } = setup({ cpuPercent: 240 })
    expect(within(card).getByTestId('cpu-bar')).toHaveStyle({ width: '100%' })
  })

  it('shows dashes and no bars while CPU and RAM are unknown', () => {
    const { card } = setup({ cpuPercent: null, memBytes: null })
    const strip = within(card).getByTestId('card-strip')
    expect(within(strip).getAllByText('—')).toHaveLength(3)
    expect(within(card).queryByTestId('cpu-bar')).toBeNull()
    expect(within(card).queryByTestId('ram-bar')).toBeNull()
  })

  it('shows the uptime of a running project', () => {
    const upSince = new Date(Date.now() - (2 * 60 + 5) * 60_000).toISOString()
    const { card } = setup({ upSince })
    const strip = within(card).getByTestId('card-strip')
    expect(strip).toHaveTextContent('Uptime')
    expect(strip).toHaveTextContent('2h 5m')
  })

  it('gives an Error card the red edge and its Issue instead of the uptime', () => {
    const { card } = setup({
      status: 'error',
      actions: ['start', 'restart'],
      containers: [makeContainer('web', { restartCount: 14 })],
    })
    expect(card.className).toContain('border-error-border')
    const strip = within(card).getByTestId('card-strip')
    expect(strip).toHaveTextContent('Issue')
    expect(strip).not.toHaveTextContent('Uptime')
    expect(within(strip).getByText('Restarted 14 times')).toHaveClass('text-danger-text')
  })

  it('keeps the plain edge on a card that is not in error', () => {
    const { card } = setup()
    expect(card.className).not.toContain('border-error-border')
  })

  describe('last backup', () => {
    const lastLine = (card: HTMLElement) => within(card).getByTestId('last-backup')

    it('shows an ok backup with a green dot', () => {
      const { card } = setup()
      expect(card).toHaveTextContent('Last backup')
      expect(lastLine(card)).toHaveTextContent('02:00 today')
      expect(within(lastLine(card)).getByTestId('backup-dot')).toHaveClass('bg-success')
    })

    it('shows a failed backup with a red dot and its state', () => {
      const { card } = setup({}, { latest: { text: 'Yesterday 02:00 · failed', tone: 'bad' } })
      expect(lastLine(card)).toHaveTextContent('Yesterday 02:00 · failed')
      expect(within(lastLine(card)).getByTestId('backup-dot')).toHaveClass('bg-danger')
    })

    it('says No backups yet with a grey dot, not a dash', () => {
      const { card } = setup({}, { latest: { text: 'No backups yet', tone: 'none' } })
      expect(lastLine(card)).toHaveTextContent('No backups yet')
      expect(lastLine(card)).not.toHaveTextContent('—')
      expect(within(lastLine(card)).getByTestId('backup-dot')).toHaveClass('bg-[#4a515c]')
    })

    it('shows a dash with no dot when the read failed', () => {
      const { card } = setup({}, { latest: null })
      expect(lastLine(card)).toHaveTextContent('—')
      expect(within(lastLine(card)).queryByTestId('backup-dot')).toBeNull()
    })
  })

  describe('controls', () => {
    it('wires the switch and its On word', () => {
      const { item, card } = setup()
      expect(within(card).getByText('On')).toBeInTheDocument()
      fireEvent.click(within(card).getByRole('switch', { name: 'Stop shop' }))
      expect(item.onStop).toHaveBeenCalledTimes(1)
    })

    it('wires the menu: Start, Restart, Back up now, View logs, Open details', () => {
      const { item, card } = setup({ actions: ['stop', 'restart', 'untrack'] })
      fireEvent.click(within(card).getByRole('button', { name: 'More actions for shop' }))
      expect(screen.getAllByRole('menuitem').map((m) => m.textContent)).toEqual([
        'Start',
        'Restart',
        'Back up now',
        'View logs',
        'Open details',
        'Stop tracking',
      ])
      expect(screen.getByRole('menuitem', { name: 'View logs' })).toHaveAttribute(
        'href',
        '/projects/7/logs',
      )
      expect(screen.getByRole('menuitem', { name: 'Open details' })).toHaveAttribute(
        'href',
        '/projects/7',
      )
      fireEvent.click(screen.getByRole('menuitem', { name: 'Restart' }))
      expect(item.onRestart).toHaveBeenCalledTimes(1)
    })

    it('runs Back up now and Stop tracking from the menu', () => {
      const { item, card } = setup({ actions: ['stop', 'restart', 'untrack'] })
      const open = () =>
        fireEvent.click(within(card).getByRole('button', { name: 'More actions for shop' }))
      open()
      fireEvent.click(screen.getByRole('menuitem', { name: 'Back up now' }))
      expect(item.backup.onRun).toHaveBeenCalledTimes(1)
      open()
      fireEvent.click(screen.getByRole('menuitem', { name: 'Stop tracking' }))
      expect(item.onUntrack).toHaveBeenCalledTimes(1)
    })

    it('disables View logs for a Not found project, with the reason', () => {
      const { card } = setup({ status: 'notfound', actions: ['remove'] })
      fireEvent.click(within(card).getByRole('button', { name: 'More actions for shop' }))
      const logs = screen.getByRole('menuitem', { name: 'View logs' })
      expect(logs).toHaveAttribute('aria-disabled', 'true')
      expect(logs).toHaveAttribute('title', 'The project folder is missing')
      expect(logs).not.toHaveAttribute('href')
      expect(screen.getByRole('menuitem', { name: 'Open details' })).toHaveAttribute(
        'href',
        '/projects/7',
      )
    })
  })
})
