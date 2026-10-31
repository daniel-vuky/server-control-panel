import { act } from '@testing-library/react'
import { useLocation } from 'react-router'
import { vi } from 'vitest'
import type {
  Backup,
  BackupBatch,
  Container,
  Database,
  DriveStatus,
  Project,
} from '../api/types.gen'
import { projectRoute } from '../app/App'
import { FakeEventSource } from './fakeEventSource'
import { renderApp } from './renderApp'

/** makeProject is a tracked, running project with no containers; overrides win. */
export function makeProject(overrides: Partial<Project> = {}): Project {
  return {
    id: 1,
    displayName: 'shop',
    path: '/srv/work/shop',
    workspaceId: 1,
    self: false,
    status: 'running',
    actions: ['stop', 'restart', 'untrack'],
    databases: [],
    missingSince: '',
    publicDomain: '',
    stopTemplate: 'stop',
    alertCpuPercent: null,
    alertRamMb: null,
    composeFiles: ['compose.yaml'],
    cpuPercent: null,
    memBytes: null,
    upSince: '',
    containers: [],
    driveFolder: 'shop',
    ...overrides,
  }
}

/** makeDatabase is a container Postgres database; key defaults from type, location and name, backup and schedule to null. */
export function makeDatabase(overrides: Partial<Database> = {}): Database {
  const d = {
    type: 'postgresql',
    name: 'shop',
    locationKind: 'container',
    location: 'db',
    sources: [],
    ...overrides,
  }
  return { key: `${d.type}:${d.location}:${d.name}`, backup: null, schedule: null, ...d }
}

/**
 * makeBackup is backup 7 of shop's Postgres database, queued first in line,
 * with its four steps waiting; overrides win.
 */
export function makeBackup(overrides: Partial<Backup> = {}): Backup {
  return {
    id: 7,
    projectId: 1,
    projectName: 'shop',
    database: {
      type: 'postgresql',
      name: 'shop',
      locationKind: 'container',
      location: 'db',
      key: 'postgresql:db:shop',
    },
    state: 'queued',
    failedStep: '',
    steps: ['dump', 'compress', 'upload', 'prune'].map((step) => ({
      step,
      state: 'waiting',
      detail: '',
    })),
    error: '',
    hint: '',
    rawBytes: 0,
    sizeBytes: 0,
    fileName: '',
    driveLink: '',
    startedBy: 'dev@localhost',
    createdAt: '2026-10-31T10:00:00Z',
    startedAt: '',
    finishedAt: '',
    durationMs: 0,
    queuePosition: 1,
    prunedAt: null,
    ...overrides,
  }
}

/** makeContainer is a running container of service `service`; overrides win. */
export function makeContainer(service: string, overrides: Partial<Container> = {}): Container {
  return {
    name: `shop-${service}-1`,
    service,
    state: 'running',
    status: 'Running',
    image: `${service}:1.0`,
    ports: [],
    cpuPercent: null,
    memBytes: null,
    restartCount: 0,
    startedAt: '',
    exitReason: '',
    health: '',
    ...overrides,
  }
}

/**
 * isChartRead says a fetch is one of the Overview charts' reads (the project's
 * series and the server zone), which tests of other parts of the page leave
 * unanswered with `unanswered`.
 */
export const isChartRead = (url: string) =>
  /^\/api\/projects\/\d+\/metrics\?/.test(url) || url === '/api/backup-settings'

/** unanswered is a fetch that never settles, or rejects as aborted when its signal aborts. */
export function unanswered(init?: RequestInit): Promise<Response> {
  return new Promise((_, reject) => {
    init?.signal?.addEventListener('abort', () =>
      reject(new DOMException('The operation was aborted.', 'AbortError')),
    )
  })
}

/**
 * leaveChartsUnanswered stubs fetch as the setup does (401), except the
 * Overview charts' reads, which never settle: for tests of other parts of
 * the project page.
 */
export function leaveChartsUnanswered() {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) =>
      isChartRead(url)
        ? unanswered(init)
        : new Response('{"error":"unauthorized"}', { status: 401 }),
    ),
  )
}

/** Where shows the path it is mounted on, so a test can see where the page navigated. */
function Where() {
  const loc = useLocation()
  return <div data-testid="where">{`${loc.pathname}${loc.search}`}</div>
}

/**
 * renderProject renders the project routes at path (with a catch-all that
 * shows where the page went). Install FakeEventSource as EventSource first.
 */
export function renderProject(path: string) {
  return renderApp(path, { routes: [projectRoute, { path: '*', element: <Where /> }] })
}

/** pushState sends one live state on the app's /api/events source. */
export function pushState(
  projects: Project[],
  dockerUp = true,
  extra: { backups?: Backup[]; batch?: BackupBatch | null; drive?: DriveStatus } = {},
) {
  act(() => {
    const es = FakeEventSource.latest('/api/events')
    if (!es) throw new Error('no /api/events source')
    if (es.readyState === FakeEventSource.CONNECTING) es.open()
    es.emit('state', {
      dockerUp,
      projects,
      backups: extra.backups ?? [],
      batch: extra.batch ?? null,
      drive: extra.drive ?? { state: 'not_connected', email: '', folder: null, reason: '' },
    })
  })
}
