import { describe, expect, it } from 'vitest'
import { allowedActions, disabledReason, inFlightStatus } from './status'

describe('status helpers', () => {
  it('maps an action to its in-flight status', () => {
    expect(inFlightStatus('start')).toBe('starting')
    expect(inFlightStatus('stop')).toBe('stopping')
    expect(inFlightStatus('restart')).toBe('restarting')
  })

  it('allows what the server allows', () => {
    expect(allowedActions('running', false, true)).toEqual(['stop', 'restart'])
    expect(allowedActions('partial', false, true)).toEqual(['start', 'stop', 'restart'])
    expect(allowedActions('stopped', false, true)).toEqual(['start'])
    expect(allowedActions('error', false, true)).toEqual(['start', 'restart'])
    expect(allowedActions('stopping', false, true)).toEqual([])
    expect(allowedActions('unknown', false, true)).toEqual([])
    expect(allowedActions('running', true, true)).toEqual([])
    expect(allowedActions('running', false, false)).toEqual([])
  })

  it('names the reason in the order the design gives', () => {
    expect(disabledReason('stopping', true, true)).toBe('Locked while a command runs')
    expect(disabledReason('running', true, true)).toBe('Monitor only')
    expect(disabledReason('running', false, true)).toBe('Docker is not responding')
    expect(disabledReason('running')).toBe('Not allowed while Running')
  })
})
