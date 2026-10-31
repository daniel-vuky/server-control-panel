import { describe, expect, it, vi } from 'vitest'
import {
  getMetricsSettings,
  getProjectMetrics,
  getServer,
  getServerMetrics,
  getServerUsage,
  saveMetricsSettings,
} from './metrics'

const json = (code: number, body: unknown) => new Response(JSON.stringify(body), { status: code })

function answer(body: unknown, code = 200) {
  const stub = vi.fn(async () => json(code, body))
  vi.stubGlobal('fetch', stub)
  return stub
}

const call = (stub: ReturnType<typeof vi.fn>) => {
  const [url, init] = stub.mock.calls[0] as unknown as [string, RequestInit]
  return {
    url,
    method: init.method ?? 'GET',
    body: init.body ? JSON.parse(String(init.body)) : null,
  }
}

const series = {
  range: '1h',
  stepSeconds: 30,
  from: '2030-01-15T16:00:30+07:00',
  to: '2030-01-15T17:00:00+07:00',
  historyStart: null,
  latest: null,
  points: [],
}

describe('metrics API', () => {
  it('reads the server info and refuses an answer without its cards', async () => {
    const info = {
      available: true,
      docker: { state: 'running', version: '29.3.0', containersRunning: 7 },
      thresholds: { cpuPercent: 90, ramPercent: 90, diskPercent: 85 },
      disks: [],
    }
    let stub = answer(info)
    await expect(getServer()).resolves.toEqual(info)
    expect(call(stub).url).toBe('/api/server')
    stub = answer({ available: true })
    await expect(getServer()).rejects.toThrow('Unexpected response from the server.')
  })

  it('reads the server and project series for a range', async () => {
    let stub = answer(series)
    await expect(getServerMetrics('24h')).resolves.toEqual(series)
    expect(call(stub).url).toBe('/api/server/metrics?range=24h')
    stub = answer(series)
    await getProjectMetrics(4, '7d')
    expect(call(stub).url).toBe('/api/projects/4/metrics?range=7d')
    stub = answer({ range: '1h' })
    await expect(getServerMetrics('1h')).rejects.toThrow('Unexpected response from the server.')
  })

  it('reads the usage report', async () => {
    const report = {
      at: null,
      totals: { cpuPercent: null, memBytes: null, diskUsedBytes: 500, netBps: null },
      rows: [],
    }
    const stub = answer(report)
    await expect(getServerUsage()).resolves.toEqual(report)
    expect(call(stub).url).toBe('/api/server/usage')
  })

  it('reads and saves the retention', async () => {
    let stub = answer({ retentionDays: 7 })
    await expect(getMetricsSettings()).resolves.toEqual({ retentionDays: 7 })
    expect(call(stub).url).toBe('/api/metrics-settings')
    stub = answer({ retentionDays: 14 })
    await expect(saveMetricsSettings(14)).resolves.toEqual({ retentionDays: 14 })
    expect(call(stub)).toEqual({
      url: '/api/metrics-settings',
      method: 'POST',
      body: { retentionDays: 14 },
    })
  })

  it('keeps the field error of a refused save', async () => {
    answer(
      {
        error: 'Enter a whole number of days from 7 to 30.',
        field: 'retentionDays',
        code: 'range',
      },
      422,
    )
    await expect(saveMetricsSettings(31)).rejects.toMatchObject({
      status: 422,
      body: { field: 'retentionDays', code: 'range' },
    })
  })
})
