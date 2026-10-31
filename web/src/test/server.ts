import type { ServerInfo, ServerPoint, ServerSeries, UsageReport, UsageRow } from '../api/types.gen'

export const MB = 1024 * 1024
export const GB = 1024 * MB

/** makeServerInfo is a 4-core, 8 GB server with Docker and cloudflared up and one disk; overrides win. */
export function makeServerInfo(overrides: Partial<ServerInfo> = {}): ServerInfo {
  return {
    available: true,
    hostname: 'prod-01',
    os: 'Ubuntu 24.04 LTS',
    kernel: '6.8.0-45-generic',
    cores: 4,
    ramBytes: 8 * GB,
    docker: { state: 'running', version: '29.3.1', containersRunning: 7 },
    cloudflared: { state: 'connected', hostnames: 5 },
    disks: [{ mount: '/', usedBytes: 92 * GB, totalBytes: 160 * GB }],
    disksPartial: false,
    thresholds: { cpuPercent: 90, ramPercent: 90, diskPercent: 85 },
    sampleSeconds: 30,
    retentionDays: 7,
    timeZone: { name: 'UTC', offset: '+00:00' },
    load: { one: 1.36, five: 1.12, fifteen: 0.98 },
    bootTime: '2026-10-20T08:00:00Z',
    ...overrides,
  }
}

const point = (t: string, v: Partial<ServerPoint> = {}): ServerPoint => ({
  t,
  cpu: 34,
  memUsed: 5 * GB,
  memTotal: 8 * GB,
  diskRead: 1.5 * MB,
  diskWrite: 0.5 * MB,
  netIn: 2 * MB,
  netOut: 1 * MB,
  load1: 1.36,
  diskUsed: 92 * GB,
  diskTotal: 160 * GB,
  tunnel: 1,
  ...v,
})

/**
 * makeSeries is a 24-hour server series of three 5-minute buckets ending
 * 2026-10-31 10:00 UTC, with a fresh latest sample; overrides win.
 */
export function makeSeries(overrides: Partial<ServerSeries> = {}): ServerSeries {
  return {
    range: '24h',
    stepSeconds: 300,
    from: '2026-10-31T09:50:00Z',
    to: '2026-10-31T10:00:00Z',
    historyStart: '2026-10-20T00:00:00Z',
    latest: {
      at: '2026-10-31T10:00:30Z',
      cpu: 34,
      memUsed: 5.2 * GB,
      memTotal: 8 * GB,
      diskRead: 1.5 * MB,
      diskWrite: 0.9 * MB,
      netIn: 2 * MB,
      netOut: 1.1 * MB,
      load1: 1.36,
      diskUsed: 92 * GB,
      diskTotal: 160 * GB,
      tunnel: 1,
    },
    points: [
      point('2026-10-31T09:50:00Z'),
      point('2026-10-31T09:55:00Z', { cpu: 40 }),
      point('2026-10-31T10:00:00Z', { cpu: 30 }),
    ],
    ...overrides,
  }
}

/** makeUsageRow is project 1 "shop", running, with every value known; overrides win. */
export function makeUsageRow(overrides: Partial<UsageRow> = {}): UsageRow {
  return {
    projectId: 1,
    name: 'shop',
    status: 'running',
    self: false,
    cpuPercent: 12,
    memBytes: 640 * MB,
    netBps: 1 * MB,
    diskBytes: 4 * GB,
    diskPartial: false,
    diskAt: '2026-10-31T09:00:00Z',
    ...overrides,
  }
}

/** makeUsage is the usage report of the given rows against round server totals. */
export function makeUsage(rows: UsageRow[]): UsageReport {
  return {
    at: '2026-10-31T10:00:30Z',
    totals: { cpuPercent: 40, memBytes: 6400 * MB, diskUsedBytes: 100 * GB, netBps: 4 * MB },
    rows,
  }
}
