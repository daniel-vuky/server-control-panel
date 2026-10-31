import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { ServerInfo, ServerSeries } from '../../api/types.gen'
import { GB, makeSeries, makeServerInfo } from '../../test/server'
import ServerCharts from './ServerCharts'

function renderCharts(series: ServerSeries = makeSeries(), info: ServerInfo = makeServerInfo()) {
  render(<ServerCharts series={series} info={info} />)
  return screen.getByRole('region', { name: 'Resource charts' })
}

const chart = (title: string) => screen.getByTestId(`chart-${title}`)

describe('ServerCharts', () => {
  it('draws the four charts over the period', () => {
    renderCharts()
    expect(screen.getByRole('img', { name: 'CPU chart, last 24 hours' })).toBeInTheDocument()
    expect(screen.getByRole('img', { name: 'RAM chart, last 24 hours' })).toBeInTheDocument()
    expect(
      screen.getByRole('img', { name: 'Disk I/O chart, Read and Write, last 24 hours' }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('img', { name: 'Network chart, In and Out, last 24 hours' }),
    ).toBeInTheDocument()
  })

  it('labels the axis by the period the series was read over', () => {
    renderCharts(makeSeries({ range: '7d' }))
    expect(screen.getByRole('img', { name: 'CPU chart, last 7 days' })).toBeInTheDocument()
    expect(chart('CPU')).toHaveTextContent('−7 days')
  })

  it('CPU shows the latest share, the cores and the threshold line', () => {
    renderCharts()
    const cpu = chart('CPU')
    expect(cpu).toHaveTextContent('34%')
    expect(within(cpu).getByText('average of 4 vCPU · threshold 90%')).toBeInTheDocument()
    // 90 of a 0–100 scale at height 180.
    expect(cpu.querySelector('line[data-threshold]')).toHaveAttribute('y1', '18.8')
    expect(cpu.querySelectorAll('polyline[data-series="CPU"]')).toHaveLength(1)
  })

  it('RAM shows used / total, its share and the threshold, the total as the top', () => {
    renderCharts()
    const ram = chart('RAM')
    expect(within(ram).getByText('5.2 GB / 8.0 GB')).toBeInTheDocument()
    expect(ram).toHaveTextContent('65% · threshold 90%')
    // The live percent and the used / total pair sit in fixed-width slots.
    expect(within(ram).getByText('65%')).toHaveClass('min-w-[4ch]', 'text-right')
    expect(within(ram).getByTestId('chart-current')).toHaveClass('min-w-[15ch]')
    // 90% of the 8 GB total, on a scale whose top is the total.
    expect(ram.querySelector('line[data-threshold]')).toHaveAttribute('y1', '18.8')
    expect(ram).toHaveTextContent('4.0 GB')
  })

  it('RAM falls back to the server total without a latest sample', () => {
    renderCharts(makeSeries({ latest: null }), makeServerInfo({ ramBytes: 16 * GB }))
    const ram = chart('RAM')
    expect(within(ram).getByTestId('chart-current')).toHaveTextContent('—')
    expect(ram).toHaveTextContent('— · threshold 90%')
    expect(ram.querySelector('line[data-threshold]')).toBeInTheDocument()
    expect(ram).toHaveTextContent('16 GB')
  })

  it('Disk I/O and Network have two named series and sum them for now', () => {
    renderCharts()
    const io = chart('Disk I/O')
    expect(io).toHaveTextContent('2.4 MB/s')
    expect(within(io).getByText('read + write')).toBeInTheDocument()
    expect(io.querySelector('polyline[data-series="Read"]')).toBeInTheDocument()
    expect(io.querySelector('polyline[data-series="Write"]')).toHaveAttribute(
      'stroke-dasharray',
      '4 3',
    )
    expect(io.querySelector('line[data-threshold]')).toBeNull()

    const net = chart('Network')
    expect(net).toHaveTextContent('3.1 MB/s')
    expect(within(net).getByText('in + out')).toBeInTheDocument()
    expect(net.querySelector('polyline[data-series="In"]')).toBeInTheDocument()
    expect(net.querySelector('polyline[data-series="Out"]')).toHaveAttribute(
      'stroke-dasharray',
      '4 3',
    )
    expect(net.querySelector('line[data-threshold]')).toBeNull()
  })

  it('reads — for every current value without a sample of the last 90 s', () => {
    renderCharts(makeSeries({ latest: null }))
    for (const title of ['CPU', 'Disk I/O', 'Network']) {
      expect(within(chart(title)).getByText('—')).toBeInTheDocument()
    }
  })

  it('reads — for a rate when one half of it is unknown', () => {
    const s = makeSeries()
    renderCharts(makeSeries({ latest: s.latest && { ...s.latest, netOut: null } }))
    expect(within(chart('Network')).getByText('—')).toBeInTheDocument()
  })
})
