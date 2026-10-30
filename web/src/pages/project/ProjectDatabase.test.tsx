import { screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { FakeEventSource } from '../../test/fakeEventSource'
import { makeProject, pushState, renderProject } from '../../test/projects'

beforeEach(() => {
  FakeEventSource.instances = []
  vi.stubGlobal('EventSource', FakeEventSource)
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('database and backup tab', () => {
  it('database tab lists detected databases and the backups empty state', () => {
    renderProject('/projects/1/database')
    pushState([
      makeProject({
        databases: [
          {
            type: 'postgresql',
            name: 'shop_production',
            locationKind: 'container',
            location: 'db',
            sources: ['DATABASE_URL', 'POSTGRES_DB'],
          },
          {
            type: 'sqlite',
            name: 'cache',
            locationKind: 'file',
            location: './data/cache.db',
            sources: [],
          },
          {
            type: 'mysql',
            name: 'legacy',
            locationKind: 'external',
            location: 'db.example.com',
            sources: ['MYSQL_HOST'],
          },
        ],
      }),
    ])
    const card = screen.getByRole('region', { name: 'Detected databases' })
    const items = within(card).getAllByRole('listitem')
    expect(items).toHaveLength(3)

    expect(items[0]).toHaveTextContent('shop_production')
    expect(items[0]).toHaveTextContent(
      'PostgreSQL · in container db · from DATABASE_URL, POSTGRES_DB',
    )
    expect(items[0]).toHaveTextContent('Password hidden')

    expect(items[1]).toHaveTextContent('SQLite · file ./data/cache.db')
    // A SQLite file has no password to hide.
    expect(items[1]).not.toHaveTextContent('Password hidden')

    expect(items[2]).toHaveTextContent('MySQL · external db.example.com · from MYSQL_HOST')

    const backups = screen.getByRole('region', { name: 'Backups' })
    expect(backups).toHaveTextContent('Backups arrive with the backup flow.')
    expect(within(backups).getByRole('link', { name: '#6' })).toHaveAttribute(
      'href',
      'https://github.com/daniel-vuky/server-control-panel/issues/6',
    )
    // Nothing on this tab can be changed in this flow.
    expect(screen.queryByRole('textbox')).toBeNull()
    expect(within(card).queryByRole('button')).toBeNull()
  })

  it('says when no database was detected', () => {
    renderProject('/projects/1/database')
    pushState([makeProject()])
    const card = screen.getByRole('region', { name: 'Detected databases' })
    expect(card).toHaveTextContent('No databases detected in this project.')
    expect(within(card).queryByRole('listitem')).toBeNull()
  })
})
