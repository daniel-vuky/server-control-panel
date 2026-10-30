import { act } from '@testing-library/react'
import { useLocation } from 'react-router'
import type { Backup, Container, Database, DriveStatus, Project } from '../api/types.gen'
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
    ...overrides,
  }
}

/** makeDatabase is a container Postgres database; key defaults from type, location and name, backup to null. */
export function makeDatabase(overrides: Partial<Database> = {}): Database {
  const d = {
    type: 'postgresql',
    name: 'shop',
    locationKind: 'container',
    location: 'db',
    sources: [],
    ...overrides,
  }
  return { key: `${d.type}:${d.location}:${d.name}`, backup: null, ...d }
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
  extra: { backups?: Backup[]; drive?: DriveStatus } = {},
) {
  act(() => {
    const es = FakeEventSource.latest('/api/events')
    if (!es) throw new Error('no /api/events source')
    if (es.readyState === FakeEventSource.CONNECTING) es.open()
    es.emit('state', {
      dockerUp,
      projects,
      backups: extra.backups ?? [],
      drive: extra.drive ?? { state: 'not_connected', email: '', folder: null, reason: '' },
    })
  })
}
