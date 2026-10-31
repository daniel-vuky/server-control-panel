import { render, screen } from '@testing-library/react'
import type { ComponentProps } from 'react'
import { describe, expect, it } from 'vitest'
import { formatBytes, formatPercent, formatRate } from '../lib/format'
import type { RangeKey } from '../lib/range'
import MetricChart, { type ChartSeries } from './MetricChart'

const MB = 1024 * 1024
const UTC = { name: 'UTC', offset: '+00:00' }
const SAIGON = { name: 'Asia/Ho_Chi_Minh', offset: '+07:00' }

const cpu = (values: (number | null)[]): ChartSeries => ({ name: 'CPU', values, color: '#5AA7FF' })

function chart(props: Partial<ComponentProps<typeof MetricChart>> = {}) {
  return render(
    <MetricChart
      title="CPU"
      current="34%"
      sub="average of 4 vCPU · threshold 90%"
      series={[cpu([30, 34, 38, 34])]}
      formatValue={(v) => formatPercent(v)}
      range="24h"
      historyStart={null}
      from="2025-10-28T05:00:00Z"
      zone={UTC}
      height={180}
      {...props}
    />,
  )
}

const lines = (c: HTMLElement, name: string) =>
  Array.from(c.querySelectorAll<SVGPolylineElement>(`polyline[data-series="${name}"]`))

describe('MetricChart', () => {
  it('shows the title, the current value and the sub-line', () => {
    chart()
    expect(screen.getAllByText('CPU')[0]).toHaveClass('font-semibold')
    expect(screen.getByText('34%')).toBeInTheDocument()
    expect(screen.getByText('average of 4 vCPU · threshold 90%')).toBeInTheDocument()
  })

  it('keeps the current value in a fixed-width, right-aligned box', () => {
    const { rerender } = chart()
    expect(screen.getByTestId('chart-current')).toHaveClass('min-w-[9ch]', 'text-right')
    rerender(
      <MetricChart
        title="RAM"
        current="5.2 GB / 15 GB"
        currentWidth={15}
        series={[cpu([1, 2])]}
        formatValue={(v) => String(v)}
        range="1h"
        historyStart={null}
        from="2025-10-28T05:00:00Z"
        zone={UTC}
        height={120}
      />,
    )
    expect(screen.getByTestId('chart-current')).toHaveClass('min-w-[15ch]', 'text-right')
  })

  it('is an image named after the chart and the period', () => {
    chart({ range: '7d' })
    expect(screen.getByRole('img', { name: 'CPU chart, last 7 days' })).toBeInTheDocument()
  })

  it('names both series in the image label', () => {
    chart({
      title: 'Disk I/O',
      series: [
        { name: 'Read', values: [1, 2], color: '#5AA7FF' },
        { name: 'Write', values: [2, 1], color: '#F0913A', dashed: true },
      ],
    })
    expect(
      screen.getByRole('img', { name: 'Disk I/O chart, Read and Write, last 24 hours' }),
    ).toBeInTheDocument()
  })

  it('splits the line at a gap', () => {
    const { container } = chart({ series: [cpu([10, 20, null, 30, 40])] })
    const parts = lines(container, 'CPU')
    expect(parts).toHaveLength(2)
    expect(parts[0].getAttribute('points')?.split(' ')).toHaveLength(2)
    expect(parts[1].getAttribute('points')?.split(' ')).toHaveLength(2)
    // The area under the first series breaks with it.
    expect(container.querySelectorAll('polygon[data-area]')).toHaveLength(2)
  })

  it('draws a point alone between two gaps as a short visible segment', () => {
    const { container } = chart({ series: [cpu([10, null, 50, null, 20, 30])] })
    const parts = lines(container, 'CPU')
    expect(parts).toHaveLength(3)
    const [a, b] = parts[1].getAttribute('points')?.split(' ') ?? []
    const [ax, ay] = a.split(',').map(Number)
    const [bx, by] = b.split(',').map(Number)
    expect(bx - ax).toBeGreaterThan(0)
    expect(ay).toBe(by)
    expect((ax + bx) / 2).toBeCloseTo(240, 0)
  })

  it('draws the dashed threshold line at 90 of 100 with the scale labels', () => {
    const { container } = chart({ max: 100, threshold: 90 })
    const th = container.querySelector('line[data-threshold]')
    expect(th).not.toBeNull()
    // 180 high with 1 unit of padding: 1 + (1 - 0.9) × 178.
    expect(th).toHaveAttribute('y1', '18.8')
    expect(th).toHaveAttribute('y2', '18.8')
    expect(th).toHaveAttribute('stroke', '#7A2F36')
    expect(th).toHaveAttribute('stroke-dasharray', '6 4')
    expect(screen.getByText('100%')).toBeInTheDocument()
    expect(screen.getByText('50%')).toBeInTheDocument()
    expect(screen.getByText('0')).toBeInTheDocument()
  })

  it('has no threshold line without a threshold', () => {
    const { container } = chart()
    expect(container.querySelector('line[data-threshold]')).toBeNull()
  })

  it('rounds the scale up to a nice value above the data', () => {
    chart({
      title: 'Network',
      series: [{ name: 'In', values: [1 * MB, 3.1 * MB], color: '#5AA7FF' }],
      formatValue: formatRate,
      bytes: true,
    })
    expect(screen.getByText('4.0 MB/s')).toBeInTheDocument()
    expect(screen.getByText('2.0 MB/s')).toBeInTheDocument()
  })

  it('labels an idle chart 2, 1 and 0, never a repeated 1', () => {
    chart({ current: '0.2%', series: [cpu([0, 0.3, 0.1])] })
    expect(screen.getByText('2%')).toBeInTheDocument()
    expect(screen.getByText('1%')).toBeInTheDocument()
    expect(screen.getByText('0')).toBeInTheDocument()
  })

  it('labels an idle rate chart 2 B/s, 1 B/s and 0', () => {
    chart({
      title: 'Network',
      current: '0 B/s',
      series: [{ name: 'In', values: [0, 0], color: '#5AA7FF' }],
      formatValue: formatRate,
      bytes: true,
    })
    expect(screen.getByText('2 B/s')).toBeInTheDocument()
    expect(screen.getByText('1 B/s')).toBeInTheDocument()
  })

  it('rounds a percentage scale in tens, not in 1024s', () => {
    chart({ series: [cpu([400, 1100])] })
    expect(screen.getByText('2000%')).toBeInTheDocument()
    expect(screen.getByText('1000%')).toBeInTheDocument()
  })

  it('uses a given max as the top unchanged, the threshold drawn against it', () => {
    const GB = 1024 * MB
    const { container } = chart({
      title: 'RAM',
      series: [{ name: 'Used', values: [5 * GB, 6 * GB], color: '#F0913A' }],
      max: 16 * GB,
      threshold: 0.9 * 16 * GB,
      formatValue: formatBytes,
      bytes: true,
    })
    expect(screen.getByText('16 GB')).toBeInTheDocument()
    expect(screen.getByText('8.0 GB')).toBeInTheDocument()
    expect(container.querySelector('line[data-threshold]')).toHaveAttribute('y1', '18.8')
  })

  it('rounds up past max when the data goes above it', () => {
    chart({ series: [cpu([90, 130])], max: 100 })
    expect(screen.getByText('200%')).toBeInTheDocument()
  })

  it('keeps the scale at least as high as the threshold', () => {
    chart({ series: [cpu([10, 12])], threshold: 90 })
    expect(screen.getByText('100%')).toBeInTheDocument()
  })

  it('has a legend naming Read and Write, the second dashed', () => {
    const { container } = chart({
      title: 'Disk I/O',
      series: [
        { name: 'Read', values: [1, 2, 3], color: '#5AA7FF' },
        { name: 'Write', values: [3, 2, 1], color: '#F0913A', dashed: true },
      ],
    })
    const read = screen.getByText('Read')
    const write = screen.getByText('Write')
    expect(read.querySelector('line')).not.toHaveAttribute('stroke-dasharray')
    expect(write.querySelector('line')).toHaveAttribute('stroke-dasharray')
    expect(lines(container, 'Read')[0]).not.toHaveAttribute('stroke-dasharray')
    expect(lines(container, 'Write')[0]).toHaveAttribute('stroke-dasharray', '4 3')
    expect(lines(container, 'Write')[0]).toHaveAttribute('stroke', '#F0913A')
  })

  it('shows the key for one series too', () => {
    const { container } = chart()
    const legend = container.querySelector('[data-legend]')
    expect(legend).not.toBeNull()
    expect(legend).toHaveTextContent('CPU')
    expect(legend?.querySelector('line')).toHaveAttribute('stroke', '#5AA7FF')
    expect(legend?.querySelector('line')).not.toHaveAttribute('stroke-dasharray')
  })

  describe('compact variant (design 02)', () => {
    const compact = (props: Partial<ComponentProps<typeof MetricChart>> = {}) =>
      chart({ variant: 'compact', height: 130, range: '1h', ...props })

    it('has no y-scale labels and gridlines at 25, 50 and 75 percent', () => {
      const { container } = compact({ formatValue: (v) => `scale ${v}` })
      expect(screen.queryByText(/^scale /)).toBeNull()
      expect(screen.queryByText('0')).toBeNull()
      const ys = Array.from(container.querySelectorAll('svg[role="img"] line')).map((l) =>
        l.getAttribute('y1'),
      )
      expect(ys).toEqual(['32.5', '65', '97.5'])
    })

    it('puts the label and the value on one row, muted and normal weight', () => {
      compact()
      const value = screen.getByTestId('chart-current')
      expect(value).toHaveTextContent('34%')
      expect(value.parentElement).toHaveClass('justify-between')
      const label = screen.getAllByText('CPU')[0]
      expect(label).toHaveClass('text-[13px]', 'text-muted')
      expect(label).not.toHaveClass('font-semibold')
    })

    it('shows start, the legend and now in the footer', () => {
      const { container } = compact({
        series: [cpu([1, 2]), { name: 'Out', values: [1, 2], color: '#F0913A', dashed: true }],
      })
      const legend = container.querySelector('[data-legend]') as HTMLElement
      const footer = legend.parentElement as HTMLElement
      expect(footer).toHaveClass('text-[11.5px]', 'justify-between')
      expect(Array.from(footer.children).map((c) => c.textContent)).toEqual([
        '−60 min',
        'CPUOut',
        'now',
      ])
    })

    it('keeps the fixed-width current value', () => {
      compact({ currentWidth: 5 })
      expect(screen.getByTestId('chart-current')).toHaveClass('min-w-[5ch]', 'text-right')
    })

    it('still says there is no data yet', () => {
      compact({ series: [cpu([null, null])], historyStart: null })
      expect(
        screen.getByText('No data yet. Samples are taken every 30 seconds.'),
      ).toBeInTheDocument()
    })

    it('still says where the kept history begins', () => {
      compact({
        series: [cpu([null, null, 20, 30])],
        historyStart: '2025-10-28T07:00:00Z',
        from: '2025-10-27T07:05:00Z',
        zone: SAIGON,
      })
      expect(screen.getByText('History begins Tue 28 Oct, 14:00')).toBeInTheDocument()
    })
  })

  it('says where the kept history begins, in the server zone', () => {
    chart({
      series: [cpu([null, null, 20, 30])],
      historyStart: '2025-10-28T07:00:00Z',
      from: '2025-10-27T07:05:00Z',
      zone: SAIGON,
    })
    expect(screen.getByText('History begins Tue 28 Oct, 14:00')).toBeInTheDocument()
  })

  it('says nothing about history that begins before the period', () => {
    chart({ historyStart: '2025-10-20T00:00:00Z', from: '2025-10-27T07:05:00Z' })
    expect(screen.queryByText(/History begins/)).toBeNull()
  })

  it('says there is no data yet when nothing was sampled', () => {
    const { container } = chart({ series: [cpu([null, null, null])] })
    expect(screen.getByText('No data yet. Samples are taken every 30 seconds.')).toBeInTheDocument()
    expect(container.querySelectorAll('polyline')).toHaveLength(0)
  })

  it('treats a period with no sample but older history as a gap, not as no data', () => {
    chart({
      series: [cpu([null, null, null])],
      historyStart: '2025-10-20T00:00:00Z',
      from: '2025-10-27T07:05:00Z',
    })
    expect(screen.queryByText(/No data yet/)).toBeNull()
  })

  it.each<[RangeKey, string, string]>([
    ['1h', '−60 min', '−30 min'],
    ['24h', '−24 h', '−12 h'],
    ['7d', '−7 days', '−3.5 days'],
  ])('labels the %s axis %s, %s, now', (range, start, mid) => {
    chart({ range })
    expect(screen.getByText(start)).toBeInTheDocument()
    expect(screen.getByText(mid)).toBeInTheDocument()
    expect(screen.getByText('now')).toBeInTheDocument()
  })

  it('uses the height for the drawing', () => {
    render(
      <MetricChart
        title="RAM"
        current="640 MB"
        series={[{ name: 'RAM', values: [1, 2], color: '#F0913A' }]}
        formatValue={(v) => String(v)}
        range="1h"
        historyStart={null}
        from="2025-10-28T05:00:00Z"
        zone={UTC}
        height={120}
      />,
    )
    const svg = screen.getByRole('img', { name: 'RAM chart, last 1 hour' })
    expect(svg).toHaveAttribute('viewBox', '0 0 600 120')
    expect(svg).toHaveAttribute('preserveAspectRatio', 'none')
    expect(svg).toHaveStyle({ height: '120px' })
  })
})
