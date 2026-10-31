import { describe, expect, it } from 'vitest'
import { makeContainer, makeProject } from '../../test/projects'
import { issueOf } from './issue'

const broken = (containers = [makeContainer('web')]) => makeProject({ status: 'error', containers })

describe('issueOf', () => {
  it('names the highest restart count first', () => {
    const p = broken([
      makeContainer('web', { restartCount: 3, exitReason: 'exit 1' }),
      makeContainer('worker', { restartCount: 14 }),
    ])
    expect(issueOf(p)).toBe('Restarted 14 times')
  })

  it('says one restart in the singular', () => {
    expect(issueOf(broken([makeContainer('web', { restartCount: 1 })]))).toBe('Restarted 1 time')
  })

  it('names the first container with an exit reason when none restarted', () => {
    const p = broken([
      makeContainer('db'),
      makeContainer('web', { state: 'exited', exitReason: 'exit 1' }),
      makeContainer('worker', { state: 'exited', exitReason: 'OOM killed' }),
    ])
    expect(issueOf(p)).toBe('web: exit 1')
  })

  it('counts the running containers when nothing restarted or exited with a reason', () => {
    const p = broken([makeContainer('web'), makeContainer('worker', { state: 'exited' })])
    expect(issueOf(p)).toBe('1 of 2 containers running')
  })

  it('says Not running with no containers', () => {
    expect(issueOf(broken([]))).toBe('Not running')
  })
})
