import { act, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { stubPhone } from '../test/phone'
import { usePhone } from './usePhone'

function Probe() {
  return <p>{usePhone() ? 'phone' : 'wide'}</p>
}

afterEach(() => vi.unstubAllGlobals())

describe('usePhone', () => {
  it('reads wide where matchMedia does not exist', () => {
    render(<Probe />)
    expect(screen.getByText('wide')).toBeInTheDocument()
  })

  it('follows the (max-width: 639px) query, also when the window is resized', () => {
    const set = stubPhone(true)
    render(<Probe />)
    expect(screen.getByText('phone')).toBeInTheDocument()
    expect(window.matchMedia).toHaveBeenCalledWith('(max-width: 639px)')
    act(() => set(false))
    expect(screen.getByText('wide')).toBeInTheDocument()
  })
})
