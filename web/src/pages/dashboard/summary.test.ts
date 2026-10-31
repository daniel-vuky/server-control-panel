import { describe, expect, it } from 'vitest'
import { GB, makeSeries, makeServerInfo } from '../../test/server'
import { summaryTiles, tileFill } from './summary'

const now = new Date('2026-10-31T10:00:30Z')
/** latestOf is the fixture's newest sample (never null). */
const latestOf = () => {
  const l = makeSeries().latest
  if (!l) throw new Error('fixture has no latest')
  return l
}
const tile = (tiles: ReturnType<typeof summaryTiles>, key: string) => {
  const t = tiles.find((x) => x.key === key)
  if (!t) throw new Error(`no ${key} tile`)
  return t
}

describe('summaryTiles', () => {
  it('words the six tiles from the facts and the newest sample', () => {
    const tiles = summaryTiles(makeServerInfo(), makeSeries(), now)
    expect(tiles.map((t) => t.label)).toEqual([
      'CPU',
      'RAM',
      'Disk',
      'Load avg',
      'Uptime',
      'CF Tunnel',
    ])
    const by = (k: string) => {
      const t = tile(tiles, k)
      return [t.value, t.sub]
    }
    expect(by('cpu')).toEqual(['34%', '4 vCPU'])
    expect(by('ram')).toEqual(['5.2 / 8', 'GB · 65%'])
    expect(by('disk')).toEqual(['92 / 160', 'GB · 58%'])
    expect(by('load')).toEqual(['1.36', '1.12 · 0.98'])
    expect(by('uptime')).toEqual(['11 days', '2 h'])
    expect(by('tunnel')).toEqual(['Connected', '5 hostnames'])
    expect(tiles.filter((t) => t.key !== 'tunnel').every((t) => !t.over && t.flag === null)).toBe(
      true,
    )
    expect(tile(tiles, 'tunnel').flag).toEqual({ text: 'Healthy', tone: 'ok' })
  })

  it('draws the charts from the points with the colours of D8', () => {
    const tiles = summaryTiles(makeServerInfo(), makeSeries(), now)
    const cpu = tile(tiles, 'cpu')
    expect(cpu.values).toEqual([34, 40, 30])
    expect(cpu.max).toBe(100)
    expect(cpu.color).toBe('#5AA7FF')
    const ram = tile(tiles, 'ram')
    expect(ram.values).toEqual([62.5, 62.5, 62.5])
    expect(ram.color).toBe('#F0913A')
    expect(tile(tiles, 'disk').values[0]).toBeCloseTo(57.5, 5)
    expect(tile(tiles, 'disk').color).toBe('#5AA7FF')
    const load = tile(tiles, 'load')
    expect(load.values).toEqual([1.36, 1.36, 1.36])
    expect(load.max).toBe(4)
    const tun = tile(tiles, 'tunnel')
    expect(tun.values).toEqual([1, 1, 1])
    expect(tun.max).toBe(1)
    expect(tun.color).toBe('#3FCF8E')
    expect(tile(tiles, 'uptime').color).toBe('#3FCF8E')
  })

  it('lets a load above the cores raise the chart top', () => {
    const series = makeSeries()
    series.points[1] = { ...series.points[1], load1: 6.5 }
    expect(tile(summaryTiles(makeServerInfo(), series, now), 'load').max).toBe(6.5)
  })

  it('draws uptime from the boot time: hours since boot, null before it, top at the last', () => {
    const info = makeServerInfo({ bootTime: '2026-10-31T09:52:00Z' })
    const t = tile(summaryTiles(info, makeSeries(), now), 'uptime')
    expect(t.values[0]).toBeNull() // 09:50 is before the boot
    expect(t.values[1]).toBeCloseTo(3 / 60, 5)
    expect(t.values[2]).toBeCloseTo(8 / 60, 5)
    expect(t.max).toBeCloseTo(8 / 60, 5)
    expect(t.value).toBe('8 min')
    expect(t.sub).toBe('')
  })

  it('flags CPU over its threshold with >, red', () => {
    const series = makeSeries({ latest: { ...latestOf(), cpu: 95 } })
    const cpu = tile(summaryTiles(makeServerInfo(), series, now), 'cpu')
    expect(cpu.over).toBe(true)
    expect(cpu.flag).toEqual({ text: 'Over threshold', tone: 'bad' })
    expect(cpu.color).toBe('#FF6B6E')
    expect(tileFill(cpu)).toBe('rgba(242,85,90,0.14)')
    const at = makeSeries({ latest: { ...latestOf(), cpu: 90 } })
    expect(tile(summaryTiles(makeServerInfo(), at, now), 'cpu').over).toBe(false)
  })

  it('does not flag RAM at exactly its threshold, and flags it above', () => {
    const latest = { ...latestOf(), memUsed: 9 * GB, memTotal: 10 * GB }
    const at = tile(summaryTiles(makeServerInfo(), makeSeries({ latest }), now), 'ram')
    expect(at.over).toBe(false)
    expect(at.flag).toBeNull()
    const above = { ...latest, memUsed: 9.1 * GB }
    expect(
      tile(summaryTiles(makeServerInfo(), makeSeries({ latest: above }), now), 'ram').over,
    ).toBe(true)
  })

  it('flags the disk tile from any disk over, naming the fullest', () => {
    const info = makeServerInfo({
      disks: [
        { mount: '/', usedBytes: 50 * GB, totalBytes: 100 * GB },
        { mount: '/data', usedBytes: 91 * GB, totalBytes: 100 * GB },
        { mount: '/backup', usedBytes: 88 * GB, totalBytes: 100 * GB },
      ],
    })
    const disk = tile(summaryTiles(info, makeSeries(), now), 'disk')
    expect(disk.over).toBe(true)
    expect(disk.flag).toEqual({ text: 'Over threshold', tone: 'bad' })
    expect(disk.sub).toBe('/data 91%')
    expect(disk.value).toBe('92 / 160')
    // 85 exactly is not over
    const edge = makeServerInfo({
      disks: [{ mount: '/', usedBytes: 85 * GB, totalBytes: 100 * GB }],
    })
    expect(tile(summaryTiles(edge, makeSeries(), now), 'disk').over).toBe(false)
  })

  it('words the tunnel states', () => {
    const t = (state: string, hostnames: number | null) =>
      tile(
        summaryTiles(makeServerInfo({ cloudflared: { state, hostnames } }), makeSeries(), now),
        'tunnel',
      )
    const down = t('disconnected', null)
    expect([down.value, down.sub, down.over, down.color]).toEqual([
      'Disconnected',
      'cloudflared',
      true,
      '#FF6B6E',
    ])
    expect(down.flag).toEqual({ text: 'Down', tone: 'bad' })
    const off = t('not_running', null)
    expect([off.value, off.over]).toEqual(['Not running', true])
    expect(off.flag).toEqual({ text: 'Down', tone: 'bad' })
    const unknown = t('unknown', null)
    expect([unknown.value, unknown.sub, unknown.flag, unknown.over]).toEqual([
      'Unknown',
      'connection unknown',
      null,
      false,
    ])
    expect(t('connected', null).sub).toBe('cloudflared')
    expect(t('connected', 5).sub).toBe('5 hostnames')
  })

  it('shows five dashes and Not available without the host /proc, keeping the tunnel', () => {
    const info = makeServerInfo({ available: false, reason: 'no proc' })
    const tiles = summaryTiles(info, makeSeries(), now)
    for (const k of ['cpu', 'ram', 'disk', 'load', 'uptime']) {
      const x = tile(tiles, k)
      expect([x.value, x.sub, x.flag, x.over]).toEqual(['—', 'Not available', null, false])
      expect(x.values).toEqual([])
    }
    expect(tile(tiles, 'tunnel').value).toBe('Connected')
  })

  it('shows dashes where there is no sample', () => {
    const tiles = summaryTiles(makeServerInfo(), makeSeries({ latest: null }), now)
    for (const k of ['cpu', 'ram', 'disk', 'load']) expect(tile(tiles, k).value).toBe('—')
    expect(tile(tiles, 'cpu').sub).toBe('4 vCPU')
    expect(tile(tiles, 'cpu').over).toBe(false)
    expect(tile(tiles, 'uptime').value).toBe('11 days')
  })

  it('shows dashes for everything without either read', () => {
    const noSeries = summaryTiles(makeServerInfo(), null, now)
    expect(tile(noSeries, 'cpu').value).toBe('—')
    expect(tile(noSeries, 'cpu').values).toEqual([])
    expect(tile(noSeries, 'tunnel').value).toBe('Connected')
    const noInfo = summaryTiles(null, makeSeries(), now)
    expect(noInfo.map((t) => t.label).length).toBe(6)
    expect(tile(noInfo, 'uptime').value).toBe('—')
    expect(tile(noInfo, 'tunnel').value).toBe('—')
    expect(noInfo.every((t) => !t.over)).toBe(true)
  })

  it('shows dashes, not NaN, for no disks or a zero disk total', () => {
    const info = makeServerInfo({ disks: [] })
    const latest = { ...latestOf(), diskUsed: 0, diskTotal: 0 }
    const series = makeSeries({ latest })
    series.points = series.points.map((p) => ({ ...p, diskUsed: 0, diskTotal: 0 }))
    const disk = tile(summaryTiles(info, series, now), 'disk')
    expect(disk.value).toBe('—')
    expect(disk.over).toBe(false)
    expect(disk.values).toEqual([null, null, null])
    expect(`${disk.value}${disk.sub}`).not.toContain('NaN')
    // disks listed but the sample says nothing: still no NaN
    const listed = tile(summaryTiles(makeServerInfo(), series, now), 'disk')
    expect(listed.value).toBe('—')
  })

  it('shows the fallback disk row as the one disk', () => {
    const info = makeServerInfo({
      disks: [{ mount: '/ (panel data)', usedBytes: 20 * GB, totalBytes: 100 * GB }],
      disksPartial: true,
    })
    const latest = { ...latestOf(), diskUsed: 20 * GB, diskTotal: 100 * GB }
    const disk = tile(summaryTiles(info, makeSeries({ latest }), now), 'disk')
    expect([disk.value, disk.sub, disk.over]).toEqual(['20 / 100', 'GB · 20%', false])
  })

  it('has fills as design 01 gives them', () => {
    const tiles = summaryTiles(makeServerInfo(), makeSeries(), now)
    expect(tileFill(tile(tiles, 'cpu'))).toBe('rgba(90,167,255,0.12)')
    expect(tileFill(tile(tiles, 'ram'))).toBe('rgba(240,145,58,0.12)')
    expect(tileFill(tile(tiles, 'uptime'))).toBe('rgba(63,207,142,0.10)')
  })
})
