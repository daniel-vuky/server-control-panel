import type { Database } from '../api/types.gen'

const typeNames: Record<string, string> = {
  postgresql: 'PostgreSQL',
  mysql: 'MySQL',
  mongodb: 'MongoDB',
  sqlite: 'SQLite',
}

/** databaseType is the product name of a database type ("PostgreSQL"). */
export const databaseType = (type: string): string => typeNames[type] ?? type

/** formatDatabase is the one-line label of a database: its type and where it lives. */
export function formatDatabase(d: Database): string {
  const type = databaseType(d.type)
  switch (d.locationKind) {
    case 'container':
      return d.location ? `${type} · container ${d.location}` : `${type} · container`
    case 'host':
      return `${type} · host`
    case 'file':
      return `${type} · file`
    case 'external':
      return d.location ? `${type} · external ${d.location}` : `${type} · external`
    default:
      return `${type} · unknown`
  }
}

/** databasePlace is where a database lives, as the detail page says it ("in container db"). */
export function databasePlace(d: Database): string {
  switch (d.locationKind) {
    case 'container':
      return d.location ? `in container ${d.location}` : 'in a container'
    case 'host':
      return d.location ? `on host ${d.location}` : 'on the host'
    case 'file':
      return d.location ? `file ${d.location}` : 'in a file'
    case 'external':
      return d.location ? `external ${d.location}` : 'external'
    default:
      return 'location unknown'
  }
}
