import { render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import Sparkline from './Sparkline'

const draw = (values: (number | null)[], max = 10) =>
  render(<Sparkline values={values} max={max} color="#5AA7FF" fill="rgba(90,167,255,0.12)" />)

const points = (el: Element) =>
  (el.getAttribute('points') ?? '').split(' ').map((p) => p.split(',').map(Number))

describe('Sparkline', () => {
  it('breaks the line at a gap: two polylines for [1, null, 2, 3]', () => {
    const { container } = draw([1, null, 2, 3])
    const lines = container.querySelectorAll('polyline')
    expect(lines.length).toBe(2)
    expect(container.querySelectorAll('polygon').length).toBe(2)
    expect(points(lines[1]).length).toBe(2)
  })

  it('draws a lone value as one flat segment a bucket wide', () => {
    const { container } = draw([null, 5, null, null])
    const lines = container.querySelectorAll('polyline')
    expect(lines.length).toBe(1)
    const p = points(lines[0])
    expect(p.length).toBe(2)
    expect(p[0][1]).toBe(p[1][1])
    expect(p[1][0] - p[0][0]).toBeCloseTo(160 / 3, 0)
  })

  it('draws a lone value at the end by stepping back', () => {
    const { container } = draw([null, null, 5])
    const p = points(container.querySelector('polyline') as Element)
    expect(p[1][0]).toBe(160)
    expect(p[0][0]).toBe(80)
  })

  it('renders no polyline when every value is null or there are none', () => {
    expect(draw([null, null]).container.querySelectorAll('polyline').length).toBe(0)
    expect(draw([]).container.querySelectorAll('polyline').length).toBe(0)
  })

  it('scales to the max, clamps above it and survives a zero max', () => {
    const { container } = draw([0, 10, 20], 10)
    const p = points(container.querySelector('polyline') as Element)
    expect(p[0][1]).toBe(32)
    expect(p[1][1]).toBe(2)
    expect(p[2][1]).toBe(2)
    const zero = draw([0, 1], 0).container.querySelector('polyline') as Element
    expect(zero.getAttribute('points')).not.toContain('NaN')
  })

  it('is hidden from assistive tech and uses a non-scaling stroke', () => {
    const { container } = draw([1, 2])
    const svg = container.querySelector('svg') as SVGElement
    expect(svg.getAttribute('aria-hidden')).toBe('true')
    expect(svg.getAttribute('viewBox')).toBe('0 0 160 32')
    expect(svg.getAttribute('preserveAspectRatio')).toBe('none')
    const line = container.querySelector('polyline') as Element
    expect(line.getAttribute('vector-effect')).toBe('non-scaling-stroke')
    expect(line.getAttribute('stroke')).toBe('#5AA7FF')
  })
})
