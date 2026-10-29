import type { Database } from '../api/types.gen'

const typeNames: Record<string, string> = {
  postgresql: 'PostgreSQL',
  mysql: 'MySQL',
  mongodb: 'MongoDB',
  sqlite: 'SQLite',
}

/** formatDatabase is the one-line label of a database: its type and where it lives. */
export function formatDatabase(d: Database): string {
  const type = typeNames[d.type] ?? d.type
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
