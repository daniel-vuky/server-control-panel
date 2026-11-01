import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import Skeleton, { SkeletonCard, SkeletonLines } from './Skeleton'

describe('Skeleton', () => {
  it('is hidden from assistive tech and pulses', () => {
    const { container } = render(<Skeleton className="h-4 w-1/2" />)
    const el = container.firstElementChild as HTMLElement
    expect(el.getAttribute('aria-hidden')).toBe('true')
    expect(el.className).toContain('animate-scp-pulse')
    expect(el.className).toContain('h-4')
  })

  it('is an inline span with inline, to sit in a line of text', () => {
    const { container } = render(<Skeleton inline className="h-3 w-24" />)
    const el = container.firstElementChild as HTMLElement
    expect(el.tagName).toBe('SPAN')
    expect(el).toHaveClass('inline-block', 'animate-scp-pulse', 'h-3', 'w-24')
    expect(el.getAttribute('aria-hidden')).toBe('true')
  })
})

describe('SkeletonLines', () => {
  it('is a busy region of pulsing bars, labelled Loading by default', () => {
    render(<SkeletonLines lines={4} />)
    const region = screen.getByRole('status', { name: 'Loading' })
    expect(region.getAttribute('aria-busy')).toBe('true')
    expect(region.querySelectorAll('.animate-scp-pulse').length).toBe(4)
    expect(region.textContent).toBe('')
  })

  it('takes a label and the bar height', () => {
    render(<SkeletonLines label="Loading workspaces" lineClassName="h-5" />)
    const region = screen.getByRole('status', { name: 'Loading workspaces' })
    expect(region.querySelectorAll('.h-5').length).toBe(3)
  })
})

describe('SkeletonCard', () => {
  it('is a busy region labelled Loading with pulsing blocks', () => {
    render(<SkeletonCard />)
    const card = screen.getByLabelText('Loading')
    expect(card.getAttribute('aria-busy')).toBe('true')
    expect(card.querySelectorAll('.animate-scp-pulse').length).toBe(4)
  })

  it('takes its min-height from the caller', () => {
    render(<SkeletonCard minHeight="min-h-[274px]" />)
    const card = screen.getByLabelText('Loading')
    expect(card).toHaveClass('min-h-[274px]')
    expect(card).not.toHaveClass('min-h-[220px]')
  })

  it('is 220 px high by default', () => {
    render(<SkeletonCard />)
    expect(screen.getByLabelText('Loading')).toHaveClass('min-h-[220px]')
  })
})
