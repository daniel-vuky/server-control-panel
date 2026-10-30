import { describe, expect, it } from 'vitest'
import { disabledReason, inFlightStatus } from './status'

describe('status helpers', () => {
  it('maps an action to its in-flight status', () => {
    expect(inFlightStatus('start')).toBe('starting')
    expect(inFlightStatus('stop')).toBe('stopping')
    expect(inFlightStatus('restart')).toBe('restarting')
  })

  it('names the reason in the order the design gives', () => {
    expect(disabledReason('stopping', true, true)).toBe('Locked while a command runs')
    expect(disabledReason('running', true, true)).toBe('Monitor only')
    expect(disabledReason('running', false, true)).toBe('Docker is not responding')
    expect(disabledReason('running')).toBe('Not allowed while Running')
  })
})
