import type { Database } from '../api/types.gen'
import { formatDatabase } from '../lib/databases'

/** DatabaseCell lists a project's databases one per line, or "None detected". */
export default function DatabaseCell({ databases }: { databases: Database[] }) {
  if (databases.length === 0) return <span className="text-muted">None detected</span>
  return databases.map((d) => (
    <span key={`${d.type}|${d.name}|${d.locationKind}|${d.location}`}>{formatDatabase(d)}</span>
  ))
}
