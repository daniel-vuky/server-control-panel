import { render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { describe, expect, it } from 'vitest'
import type { LatestBackup } from '../../api/types.gen'
import QuickCounts from './QuickCounts'

const counts = { all: 5, running: 3, partial: 1, stopped: 0, error: 1 }
const now = new Date(2026, 9, 31, 15, 0)
const at = (day: number, h: number, m: number) => new Date(2026, 9, day, h, m).toISOString()

const latest = (state: string, when = at(31, 2, 0)): LatestBackup => ({
  id: 3,
  projectId: 1,
  projectName: 'shop',
  database: 'shop',
  state,
  at: when,
})

function setup(l: LatestBackup | null | undefined) {
  render(
    <MemoryRouter>
      <QuickCounts counts={counts} latest={l} now={now} />
    </MemoryRouter>,
  )
  return screen.getByRole('region', { name: 'Quick counts' })
}

describe('QuickCounts', () => {
  it('shows the four settled counts with their words', () => {
    const region = setup(undefined)
    expect(region).toHaveTextContent('3 running')
    expect(region).toHaveTextContent('1 partial')
    expect(region).toHaveTextContent('0 stopped')
    expect(region).toHaveTextContent('1 error')
    expect(region.querySelectorAll('[data-testid="count-dot"]')).toHaveLength(4)
  })

  it('names the latest backup and an OK badge', () => {
    const region = setup(latest('ok'))
    expect(region).toHaveTextContent('Latest backup: shop · 02:00 today')
    expect(within(region).getByText('OK')).toBeInTheDocument()
  })

  it('says yesterday, or the date for an older one', () => {
    expect(setup(latest('ok', at(30, 2, 0)))).toHaveTextContent('shop · Yesterday 02:00')
  })

  it.each([
    ['failed', 'Failed'],
    ['refused', 'Refused'],
    ['interrupted', 'Interrupted'],
  ])('shows %s as %s', (state, word) => {
    const region = setup(latest(state))
    expect(within(region).getByText(word)).toBeInTheDocument()
    expect(within(region).queryByText('OK')).toBeNull()
  })

  it('says there are no backups yet', () => {
    const region = setup(null)
    expect(region).toHaveTextContent('No backups yet')
    expect(region).not.toHaveTextContent('Latest backup:')
  })

  it('shows no backup part while the read has no answer, but still links', () => {
    const region = setup(undefined)
    expect(region).not.toHaveTextContent('Latest backup:')
    expect(region).not.toHaveTextContent('No backups yet')
    expect(within(region).getByRole('link', { name: 'View Backups →' })).toHaveAttribute(
      'href',
      '/backups',
    )
  })

  it('links to /backups beside a latest backup and beside none', () => {
    for (const l of [latest('ok'), null]) {
      const region = setup(l)
      expect(within(region).getByRole('link', { name: 'View Backups →' })).toHaveAttribute(
        'href',
        '/backups',
      )
      document.body.innerHTML = ''
    }
  })
})
