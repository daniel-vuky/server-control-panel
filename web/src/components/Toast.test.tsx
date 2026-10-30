import { act, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ToastProvider, useToast } from './Toast'

type Api = ReturnType<typeof useToast>

let api: Api

function Grab() {
  api = useToast()
  return null
}

function setup() {
  return render(
    <ToastProvider>
      <Grab />
    </ToastProvider>,
  )
}

function Trigger({ message }: { message: string }) {
  const { show } = useToast()
  return (
    <button type="button" onClick={() => show(message)}>
      go
    </button>
  )
}

const advance = (ms: number) =>
  act(() => {
    vi.advanceTimersByTime(ms)
  })

describe('Toast', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('shows a message in a status region, then removes it after 4000 ms', () => {
    render(
      <ToastProvider>
        <Trigger message="3 projects added" />
      </ToastProvider>,
    )
    expect(screen.queryByRole('status')).toBeNull()
    act(() => screen.getByRole('button', { name: 'go' }).click())
    expect(screen.getByRole('status')).toHaveTextContent('3 projects added')

    advance(3999)
    expect(screen.getByRole('status')).toHaveTextContent('3 projects added')
    advance(1)
    expect(screen.queryByRole('status')).toBeNull()
  })

  it('show is success', () => {
    setup()
    expect(api.show).toBe(api.success)
  })

  it('success disappears after 4000 ms, each toast on its own timer', () => {
    setup()
    act(() => api.success('first'))
    advance(3000)
    act(() => api.success('second'))
    expect(screen.getAllByRole('status')).toHaveLength(2)
    advance(1000)
    expect(screen.getAllByRole('status')).toHaveLength(1)
    expect(screen.getByRole('status')).toHaveTextContent('second')
    advance(3000)
    expect(screen.queryByRole('status')).toBeNull()
  })

  it('stacks the newest last', () => {
    setup()
    act(() => api.success('one'))
    act(() => api.success('two'))
    const all = screen.getAllByRole('status')
    expect(all[0]).toHaveTextContent('one')
    expect(all[1]).toHaveTextContent('two')
  })

  it('error stays after 60 s, with a bold title and a pre-line detail, until Dismiss', () => {
    setup()
    act(() => api.error("Couldn't stop shop", 'line one\nline two'))
    advance(60_000)
    const alert = screen.getByRole('alert')
    expect(alert).toHaveTextContent("Couldn't stop shop")
    expect(alert).toHaveTextContent('line one')
    expect(screen.getByText(/line one/)).toHaveClass('whitespace-pre-line')
    expect(screen.getByText(/line one/).textContent).toBe('line one\nline two')
    act(() => screen.getByRole('button', { name: 'Dismiss' }).click())
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('error without a detail shows only the title', () => {
    setup()
    act(() => api.error('Couldn’t start shop'))
    expect(screen.getByRole('alert')).toHaveTextContent('Couldn’t start shop')
  })

  it('a success after an error leaves the error visible', () => {
    setup()
    act(() => api.error('Couldn’t stop shop', 'boom'))
    act(() => api.success('Started blog'))
    advance(4000)
    expect(screen.getByRole('alert')).toBeInTheDocument()
    expect(screen.queryByRole('status')).toBeNull()
  })

  it('a fifth toast drops the oldest success, keeping errors', () => {
    setup()
    act(() => api.error('err', 'x'))
    act(() => api.success('s1'))
    act(() => api.success('s2'))
    act(() => api.success('s3'))
    act(() => api.success('s4'))
    expect(screen.getByRole('alert')).toBeInTheDocument()
    const texts = screen.getAllByRole('status').map((e) => e.textContent)
    expect(texts).toEqual(['s2', 's3', 's4'])
  })

  it('drops the oldest error only when all four are errors', () => {
    setup()
    for (const n of ['e1', 'e2', 'e3', 'e4']) act(() => api.error(n))
    act(() => api.error('e5'))
    const texts = screen.getAllByRole('alert').map((e) => e.textContent?.replace(/\s/g, ''))
    expect(texts).toHaveLength(4)
    expect(texts.join()).not.toContain('e1,')
    expect(texts[3]).toContain('e5')
  })

  it('four errors then a success shows the success and drops the oldest error', () => {
    setup()
    for (const n of ['e1', 'e2', 'e3', 'e4']) act(() => api.error(n))
    act(() => api.success('ok'))
    expect(screen.getByRole('status')).toHaveTextContent('ok')
    const errs = screen.getAllByRole('alert').map((e) => e.textContent)
    expect(errs).toHaveLength(3)
    expect(errs.join()).not.toContain('e1')
  })

  it('useToast throws outside a provider', () => {
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {})
    expect(() => render(<Trigger message="x" />)).toThrow(/ToastProvider/)
    spy.mockRestore()
  })
})
