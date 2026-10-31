import { ApiError, getJSON, postJSON } from './client'
import type {
  MetricsSettings,
  ProjectSeries,
  ServerInfo,
  ServerSeries,
  UsageReport,
} from './types.gen'

/** MetricsRange is a chart period: 1 hour of 30 s samples, 24 hours of 5 min, 7 days of 30 min. */
export type MetricsRange = '1h' | '24h' | '7d'

const unexpected = () => new ApiError(200, null, 'Unexpected response from the server.')

/** getServer is the System info and System services cards, the thresholds and the server zone. */
export async function getServer(signal?: AbortSignal): Promise<ServerInfo> {
  const s = await getJSON<ServerInfo>('/api/server', signal)
  if (
    typeof s !== 'object' ||
    s === null ||
    typeof s.docker !== 'object' ||
    s.docker === null ||
    typeof s.thresholds !== 'object' ||
    s.thresholds === null ||
    !Array.isArray(s.disks)
  ) {
    throw unexpected()
  }
  return s
}

/** getServerMetrics is the server's series over a period: one point per bucket, null in a gap. */
export async function getServerMetrics(
  range: MetricsRange,
  signal?: AbortSignal,
): Promise<ServerSeries> {
  const s = await getJSON<ServerSeries>(`/api/server/metrics?range=${range}`, signal)
  if (typeof s !== 'object' || s === null || !Array.isArray(s.points)) throw unexpected()
  return { ...s, historyStart: s.historyStart ?? null, latest: s.latest ?? null }
}

/** getServerUsage is the Usage by app table: the tracked projects, the panel row and the totals. */
export async function getServerUsage(signal?: AbortSignal): Promise<UsageReport> {
  const u = await getJSON<UsageReport>('/api/server/usage', signal)
  if (
    typeof u !== 'object' ||
    u === null ||
    !Array.isArray(u.rows) ||
    typeof u.totals !== 'object' ||
    u.totals === null
  ) {
    throw unexpected()
  }
  return { ...u, at: u.at ?? null }
}

/** getProjectMetrics is one project's series over a period; 404 for a project that is not tracked. */
export async function getProjectMetrics(
  projectId: number,
  range: MetricsRange,
  signal?: AbortSignal,
): Promise<ProjectSeries> {
  const s = await getJSON<ProjectSeries>(
    `/api/projects/${projectId}/metrics?range=${range}`,
    signal,
  )
  if (typeof s !== 'object' || s === null || !Array.isArray(s.points)) throw unexpected()
  return { ...s, historyStart: s.historyStart ?? null, latest: s.latest ?? null }
}

/** getMetricsSettings is how many days of metrics history are kept. */
export async function getMetricsSettings(signal?: AbortSignal): Promise<MetricsSettings> {
  const s = await getJSON<MetricsSettings>('/api/metrics-settings', signal)
  if (typeof s !== 'object' || s === null || typeof s.retentionDays !== 'number') throw unexpected()
  return s
}

/** saveMetricsSettings sets the days of history kept (7 to 30); 422 names retentionDays. */
export function saveMetricsSettings(days: number): Promise<MetricsSettings> {
  const body: MetricsSettings = { retentionDays: days }
  return postJSON<MetricsSettings>('/api/metrics-settings', body)
}
