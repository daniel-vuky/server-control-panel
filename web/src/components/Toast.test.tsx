import { act, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ToastProvider, useToast } from './Toast'

function Trigger({ message }: { message: string }) {
  const { show } = useToast()
  return (
    <button type="button" onClick={() => show(message)}>
      go
    </button>
  )
}

function Two() {
  const { show } = useToast()
  return (
    <>
      <button type="button" onClick={() => show('first')}>
        a
      </button>
      <button type="button" onClick={() => show('second')}>
        b
      </button>
    </>
  )
}

const clickGo = () =>
  act(() => {
    screen.getByRole('button', { name: 'go' }).click()
  })

describe('Toast', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('shows the message in a status region, then removes it after 4000 ms', () => {
    render(
      <ToastProvider>
        <Trigger message="3 projects added" />
      </ToastProvider>,
    )
    expect(screen.getByRole('status')).toBeEmptyDOMElement()
    clickGo()
    expect(screen.getByRole('status')).toHaveTextContent('3 projects added')

    act(() => {
      vi.advanceTimersByTime(3999)
    })
    expect(screen.getByRole('status')).toHaveTextContent('3 projects added')
    act(() => {
      vi.advanceTimersByTime(1)
    })
    expect(screen.getByRole('status')).toBeEmptyDOMElement()
  })

  it('shows one at a time: a new message replaces the old and restarts the timer', () => {
    render(
      <ToastProvider>
        <Two />
      </ToastProvider>,
    )
    act(() => screen.getByRole('button', { name: 'a' }).click())
    act(() => {
      vi.advanceTimersByTime(3000)
    })
    act(() => screen.getByRole('button', { name: 'b' }).click())
    expect(screen.getAllByRole('status')).toHaveLength(1)
    expect(screen.getByRole('status')).toHaveTextContent('second')
    act(() => {
      vi.advanceTimersByTime(3000)
    })
    expect(screen.getByRole('status')).toHaveTextContent('second')
    act(() => {
      vi.advanceTimersByTime(1000)
    })
    expect(screen.getByRole('status')).toBeEmptyDOMElement()
  })

  it('useToast throws outside a provider', () => {
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {})
    expect(() => render(<Trigger message="x" />)).toThrow(/ToastProvider/)
    spy.mockRestore()
  })
})
