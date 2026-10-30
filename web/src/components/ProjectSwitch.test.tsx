import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { Status } from '../lib/status'
import ProjectSwitch from './ProjectSwitch'

function setup(
  status: Status,
  actions: string[],
  extra: { self?: boolean; dockerDown?: boolean } = {},
) {
  const onStart = vi.fn()
  const onStop = vi.fn()
  render(
    <ProjectSwitch
      name="shop"
      status={status}
      actions={actions}
      onStart={onStart}
      onStop={onStop}
      {...extra}
    />,
  )
  return { onStart, onStop, sw: screen.getByRole('switch') }
}

describe('ProjectSwitch', () => {
  it.each([
    ['running', 'true', 'On', 'bg-switch-on'],
    ['partial', 'true', 'On', 'bg-switch-on'],
    ['starting', 'true', 'Starting', 'bg-switch-busy'],
    ['restarting', 'true', 'Restarting', 'bg-switch-busy'],
    ['stopping', 'false', 'Stopping', 'bg-switch-off'],
    ['stopped', 'false', 'Off', 'bg-switch-off'],
    ['error', 'false', 'Off', 'bg-switch-off'],
    ['unknown', 'false', 'Off', 'bg-switch-off'],
    ['notfound', 'false', 'Off', 'bg-switch-off'],
  ] as const)('%s: aria-checked %s, label %s, %s', (status, checked, label, bg) => {
    const { sw } = setup(status, ['start', 'stop'])
    expect(sw).toHaveAttribute('aria-checked', checked)
    expect(screen.getByText(label)).toBeInTheDocument()
    expect(sw).toHaveClass(bg)
  })

  it('names the action in its aria-label', () => {
    const { sw } = setup('running', ['stop'])
    expect(sw).toHaveAccessibleName('Stop shop')
  })

  it('reads Start when off', () => {
    const { sw } = setup('stopped', ['start'])
    expect(sw).toHaveAccessibleName('Start shop')
  })

  it('a click calls onStop when on and onStart when off', () => {
    const on = setup('running', ['start', 'stop'])
    fireEvent.click(on.sw)
    expect(on.onStop).toHaveBeenCalledTimes(1)
    expect(on.onStart).not.toHaveBeenCalled()
  })

  it('a click calls onStart when off', () => {
    const off = setup('stopped', ['start', 'stop'])
    fireEvent.click(off.sw)
    expect(off.onStart).toHaveBeenCalledTimes(1)
    expect(off.onStop).not.toHaveBeenCalled()
  })

  it('is disabled with a reason when stop is not allowed', () => {
    const { sw, onStop } = setup('running', ['start'])
    expect(sw).toBeDisabled()
    expect(sw).toHaveClass('disabled:opacity-50')
    expect(sw).toHaveAttribute('title', 'Not allowed while Running')
    fireEvent.click(sw)
    expect(onStop).not.toHaveBeenCalled()
  })

  it('is disabled when start is not allowed while off', () => {
    const { sw } = setup('stopped', ['stop'])
    expect(sw).toBeDisabled()
    expect(sw).toHaveAttribute('title', 'Not allowed while Stopped')
  })

  it('says why while a command runs', () => {
    const { sw } = setup('stopping', [])
    expect(sw).toBeDisabled()
    expect(sw).toHaveAttribute('title', 'Locked while a command runs')
  })

  it('says Monitor only for the panel itself', () => {
    const { sw } = setup('running', [], { self: true })
    expect(sw).toHaveAttribute('title', 'Monitor only')
  })

  it('says Docker is not responding when docker is down', () => {
    const { sw } = setup('stopped', [], { dockerDown: true })
    expect(sw).toHaveAttribute('title', 'Docker is not responding')
  })

  it('has no title and is enabled when allowed', () => {
    const { sw } = setup('running', ['stop'])
    expect(sw).toBeEnabled()
    expect(sw).not.toHaveAttribute('title')
  })
})
