import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import Skeleton, { SkeletonCard } from './Skeleton'

describe('Skeleton', () => {
  it('is hidden from assistive tech and pulses', () => {
    const { container } = render(<Skeleton className="h-4 w-1/2" />)
    const el = container.firstElementChild as HTMLElement
    expect(el.getAttribute('aria-hidden')).toBe('true')
    expect(el.className).toContain('animate-scp-pulse')
    expect(el.className).toContain('h-4')
  })
})

describe('SkeletonCard', () => {
  it('is a busy region labelled Loading with pulsing blocks', () => {
    render(<SkeletonCard />)
    const card = screen.getByLabelText('Loading')
    expect(card.getAttribute('aria-busy')).toBe('true')
    expect(card.querySelectorAll('.animate-scp-pulse').length).toBe(4)
  })
})
