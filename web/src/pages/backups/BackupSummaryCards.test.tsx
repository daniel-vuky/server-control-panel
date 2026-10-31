import { fireEvent, render, screen, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { BackupSummary, TimeZone } from '../../api/types.gen'
import BackupSummaryCards from './BackupSummaryCards'

const zone: TimeZone = { name: 'Asia/Ho_Chi_Minh', offset: '+07:00' }
const GB = 1024 * 1024 * 1024

const days = [
  ['2026-10-25', 3, 0],
  ['2026-10-26', 2, 1],
  ['2026-10-27', 0, 0],
  ['2026-10-28', 4, 0],
  ['2026-10-29', 3, 0],
  ['2026-10-30', 4, 1],
  ['2026-10-31', 3, 0],
].map(([date, ok, failed]) => ({
  date: date as string,
  ok: ok as number,
  failed: failed as number,
}))

const summary = (more: Partial<BackupSummary> = {}): BackupSummary => ({
  driveBytes: 1.42 * GB,
  driveFiles: 38,
  driveProjects: 3,
  days,
  ok7d: 19,
  failed7d: 2,
  recentFailures: [
    { projectId: 2, projectName: 'crm', dbName: 'crm_db', at: '2026-10-30T19:00:00Z' },
    { projectId: 1, projectName: 'shop-api', dbName: 'shop', at: '2026-10-26T01:00:00Z' },
  ],
  // 02:00 in Ho Chi Minh is 19:00 UTC the day before (the test browser runs in UTC).
  nextRun: { at: '2026-11-01T02:00:00+07:00', projects: ['shop-api', 'crm'] },
  ...more,
})

const card = (name: string) => screen.getByRole('region', { name })

describe('BackupSummaryCards', () => {
  it('keeps one sub-line per card and a skeleton as tall as a loaded card', () => {
    const { container, rerender } = render(
      <BackupSummaryCards summary={null} zone={zone} error="" onRetry={() => {}} />,
    )
    const loading = container.querySelector('section[aria-busy="true"]') as HTMLElement
    const skeletonCards = [...loading.children] as HTMLElement[]
    expect(skeletonCards).toHaveLength(4)
    rerender(<BackupSummaryCards summary={summary()} zone={zone} error="" onRetry={() => {}} />)
    for (const c of skeletonCards) expect(c).toHaveClass('min-h-[110px]')
    for (const name of ['Drive usage', 'Failed · 7 days', 'Next run']) {
      expect(card(name)).toHaveClass('min-h-[110px]')
      // The label, the value and one sub-line.
      expect(card(name).children).toHaveLength(3)
    }
  })

  it('shows what the panel stored on Drive', () => {
    render(<BackupSummaryCards summary={summary()} zone={zone} error="" onRetry={() => {}} />)
    const drive = card('Drive usage')
    expect(drive).toHaveTextContent('1.4 GB')
    expect(drive).toHaveTextContent('38 files · 3 projects')
    // One sub-line: "stored by the panel" is the card's title (a tooltip), not a second line.
    expect(drive).toHaveAttribute('title', 'Stored by the panel')
    expect(drive).not.toHaveTextContent('stored by the panel')
  })

  it('words one file of one project in the singular', () => {
    render(
      <BackupSummaryCards
        summary={summary({ driveBytes: 2048, driveFiles: 1, driveProjects: 1 })}
        zone={zone}
        error=""
        onRetry={() => {}}
      />,
    )
    expect(card('Drive usage')).toHaveTextContent('1 file · 1 project')
  })

  it('counts the last 7 days with a strip per day', () => {
    render(<BackupSummaryCards summary={summary()} zone={zone} error="" onRetry={() => {}} />)
    const ok = card('Succeeded · 7 days')
    expect(ok).toHaveTextContent('19')
    const strip = within(ok).getAllByRole('listitem')
    expect(strip.map((d) => d.getAttribute('data-state'))).toEqual([
      'ok',
      'failed',
      'none',
      'ok',
      'ok',
      'failed',
      'ok',
    ])
    expect(strip[1]).toHaveTextContent('Oct 26: 2 succeeded, 1 failed')
  })

  it('names the latest two failures in the server zone', () => {
    render(<BackupSummaryCards summary={summary()} zone={zone} error="" onRetry={() => {}} />)
    const failed = card('Failed · 7 days')
    expect(failed).toHaveTextContent('2')
    // 19:00 UTC on Oct 30 is Oct 31 in Ho Chi Minh: today there.
    expect(failed).toHaveTextContent('crm · today, shop-api · Oct 26')
  })

  it('says when nothing failed', () => {
    render(
      <BackupSummaryCards
        summary={summary({ failed7d: 0, recentFailures: [] })}
        zone={zone}
        error=""
        onRetry={() => {}}
      />,
    )
    expect(card('Failed · 7 days')).toHaveTextContent('No failures')
  })

  it('shows the next run in the server zone with its projects', () => {
    render(<BackupSummaryCards summary={summary()} zone={zone} error="" onRetry={() => {}} />)
    const next = card('Next run')
    expect(next).toHaveTextContent('02:00')
    expect(next).not.toHaveTextContent('19:00')
    // The zone joins the day line: one sub-line.
    expect(next).toHaveTextContent('Tomorrow · shop-api, crm · Asia/Ho_Chi_Minh, UTC+07:00')
  })

  it('names the day of a run further away', () => {
    render(
      <BackupSummaryCards
        summary={summary({ nextRun: { at: '2026-11-03T02:00:00+07:00', projects: ['blog'] } })}
        zone={zone}
        error=""
        onRetry={() => {}}
      />,
    )
    expect(card('Next run')).toHaveTextContent('Tue Nov 3 · blog')
  })

  it('reads the run in its own offset before the server zone is known', () => {
    render(<BackupSummaryCards summary={summary()} zone={null} error="" onRetry={() => {}} />)
    const next = card('Next run')
    expect(next).toHaveTextContent('02:00')
    expect(next).toHaveTextContent('Tomorrow · shop-api, crm')
    expect(next).toHaveTextContent('UTC+07:00')
  })

  it('says when nothing is scheduled', () => {
    render(
      <BackupSummaryCards
        summary={summary({ nextRun: null })}
        zone={zone}
        error=""
        onRetry={() => {}}
      />,
    )
    const next = card('Next run')
    expect(next).toHaveTextContent('No schedules')
    // Words, not a figure: sans 16 px / 600 instead of the 24 px mono value.
    const words = within(next).getByText('No schedules')
    expect(words).toHaveClass('text-base', 'font-semibold')
    expect(words).not.toHaveClass('font-mono', 'text-2xl')
    expect(next).toHaveTextContent("Set one in a project's Database & Backup tab.")
  })

  it('shows a loading state, then an error with Retry', () => {
    const retry = vi.fn()
    const { rerender } = render(
      <BackupSummaryCards summary={null} zone={zone} error="" onRetry={retry} />,
    )
    expect(screen.getByRole('region', { name: 'Backup summary' })).toHaveAttribute(
      'aria-busy',
      'true',
    )
    rerender(<BackupSummaryCards summary={null} zone={zone} error="boom" onRetry={retry} />)
    const alert = screen.getByRole('alert')
    expect(alert).toHaveTextContent('Could not load the backup summary.')
    fireEvent.click(within(alert).getByRole('button', { name: 'Retry' }))
    expect(retry).toHaveBeenCalled()
  })
})
