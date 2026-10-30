import { describe, expect, it } from 'vitest'
import type { Database } from '../api/types.gen'
import { makeDatabase } from '../test/projects'
import { databasePlace, formatDatabase } from './databases'

const db = (type: string, locationKind: string, location = ''): Database =>
  makeDatabase({ type, name: 'app', locationKind, location })

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

describe('databasePlace', () => {
  it.each<[Database, string]>([
    [db('postgresql', 'container', 'db'), 'in container db'],
    [db('postgresql', 'container'), 'in a container'],
    [db('mysql', 'host', 'localhost'), 'on host localhost'],
    [db('mysql', 'host'), 'on the host'],
    [db('sqlite', 'file', '/data/app.db'), 'file /data/app.db'],
    [db('mongodb', 'external', 'rds.example.com'), 'external rds.example.com'],
    [db('postgresql', 'unknown'), 'location unknown'],
  ])('%j -> %s', (d, expected) => {
    expect(databasePlace(d)).toBe(expected)
  })
})
