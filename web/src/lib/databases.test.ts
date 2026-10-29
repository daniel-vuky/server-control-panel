import { describe, expect, it } from 'vitest'
import type { Database } from '../api/types.gen'
import { formatDatabase } from './databases'

const db = (type: string, locationKind: string, location = ''): Database => ({
  type,
  name: 'app',
  locationKind,
  location,
  sources: [],
})

describe('formatDatabase', () => {
  it.each<[Database, string]>([
    [db('postgresql', 'container', 'db'), 'PostgreSQL · container db'],
    [db('mysql', 'host', 'localhost'), 'MySQL · host'],
    [db('sqlite', 'file', '/data/app.db'), 'SQLite · file'],
    [db('mongodb', 'external', 'rds.example.com'), 'MongoDB · external rds.example.com'],
    [db('postgresql', 'unknown'), 'PostgreSQL · unknown'],
  ])('%j -> %s', (d, expected) => {
    expect(formatDatabase(d)).toBe(expected)
  })
})
