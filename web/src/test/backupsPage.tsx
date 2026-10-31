import { useLocation } from 'react-router'
import { vi } from 'vitest'
import type {
  Backup,
  BackupBatch,
  BackupList,
  BackupSummary,
  DriveStatus,
  Project,
} from '../api/types.gen'
import BackupsPage from '../pages/BackupsPage'
import { makeProject, pushState } from './projects'
import { renderApp } from './renderApp'

export const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status })

export const connected: DriveStatus = {
  state: 'connected',
  email: 'me@example.com',
  folder: { id: 'f', name: 'server-backups' },
  reason: '',
}
export const drive = (state: string): DriveStatus => ({
  state,
  email: '',
  folder: null,
  reason: '',
})

export const SETTINGS = {
  defaults: { frequency: 'daily', time: '02:00', weekday: 0, keep: 14, compress: true },
  timeZone: { name: 'Asia/Ho_Chi_Minh', offset: '+07:00' },
}

export const emptySummary = (): BackupSummary => ({
  driveBytes: 0,
  driveFiles: 0,
  driveProjects: 0,
  days: ['25', '26', '27', '28', '29', '30', '31'].map((d) => ({
    date: `2026-10-${d}`,
    ok: 0,
    failed: 0,
  })),
  ok7d: 0,
  failed7d: 0,
  recentFailures: [],
  nextRun: null,
})

type Server = {
  /** The /api/backups answer for a URL (the query included); one page with `rows` by default. */
  list?: (url: string) => Response
  summary?: () => Response
  all?: () => Response
  /** Any other answer, checked first; undefined falls through. */
  other?: (url: string, init?: RequestInit) => Response | undefined
}

/**
 * serveBackups answers the Backups page's requests; the list answers `rows`
 * unless `list` says otherwise. Anything else is a 404.
 */
export function serveBackups(rows: Backup[] = [], s: Server = {}) {
  const page: BackupList = { backups: rows, more: false }
  const stub = vi.fn(async (url: string, init?: RequestInit) => {
    const extra = s.other?.(url, init)
    if (extra) return extra
    const method = init?.method ?? 'GET'
    if (method === 'POST' && url === '/api/backups/all') {
      return s.all ? s.all() : json(202, { id: 1, total: 0, skipped: 0 })
    }
    if (method !== 'GET') return json(404, { error: 'not found' })
    if (url === '/api/backup-settings') return json(200, SETTINGS)
    if (url === '/api/backups/summary') return s.summary ? s.summary() : json(200, emptySummary())
    if (url === '/api/backups' || url.startsWith('/api/backups?')) {
      return s.list ? s.list(url) : json(200, page)
    }
    return json(404, { error: 'not found' })
  })
  vi.stubGlobal('fetch', stub)
  return stub
}

/** listCalls lists the /api/backups list URLs the stub saw, in order. */
export const listCalls = (stub: ReturnType<typeof vi.fn>): string[] =>
  stub.mock.calls
    .map((c) => String(c[0]))
    .filter((u) => u === '/api/backups' || u.startsWith('/api/backups?'))

/** callsTo lists the GET URLs the stub saw that start with prefix. */
export const callsTo = (stub: ReturnType<typeof vi.fn>, prefix: string): string[] =>
  stub.mock.calls.map((c) => String(c[0])).filter((u) => u.startsWith(prefix))

function Where() {
  const loc = useLocation()
  return <div data-testid="where">{`${loc.pathname}${loc.search}`}</div>
}

export type LiveExtra = {
  backups?: Backup[]
  batch?: BackupBatch | null
  projects?: Project[]
}

/** openBackups renders /backups (path may carry a query) and sends one live state when drive is set. */
export function openBackups(state: DriveStatus | null, extra: LiveExtra = {}, path = '/backups') {
  const r = renderApp(path, {
    routes: [
      { path: '/backups', element: <BackupsPage /> },
      { path: '*', element: <Where /> },
    ],
  })
  if (state !== null) push(state, extra)
  return r
}

/** push sends one more live state. */
export function push(state: DriveStatus, extra: LiveExtra = {}) {
  pushState(extra.projects ?? [makeProject()], true, {
    drive: state,
    backups: extra.backups ?? [],
    batch: extra.batch ?? null,
  })
}

export const batch = (more: Partial<BackupBatch> = {}): BackupBatch => ({
  id: 4,
  startedBy: 'dev@localhost',
  startedAt: '2026-10-31T10:00:00Z',
  total: 6,
  done: 0,
  ok: 0,
  failed: 0,
  skipped: 0,
  finishedAt: null,
  ...more,
})
