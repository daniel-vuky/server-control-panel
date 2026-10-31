import { describe, expect, it } from 'vitest'
import { makeContainer, makeProject } from '../../test/projects'
import { stackNote } from './stack'

describe('stackNote', () => {
  it('counts the containers when all of them run', () => {
    const p = makeProject({
      containers: [makeContainer('web'), makeContainer('db'), makeContainer('worker')],
    })
    expect(stackNote(p)).toBe('Compose · 3 containers')
  })

  it('shows running of all when not every container runs', () => {
    const p = makeProject({
      containers: [makeContainer('web'), makeContainer('db', { state: 'exited' })],
    })
    expect(stackNote(p)).toBe('Compose · 1/2 containers')
  })

  it('counts a Not created row as a container that does not run', () => {
    const p = makeProject({
      containers: [makeContainer('web'), makeContainer('db', { state: '', status: 'Not created' })],
    })
    expect(stackNote(p)).toBe('Compose · 1/2 containers')
  })

  it('says one container in the singular', () => {
    expect(stackNote(makeProject({ containers: [makeContainer('web')] }))).toBe(
      'Compose · 1 container',
    )
  })

  it('is just Compose before the first live state', () => {
    expect(stackNote(makeProject({ containers: [] }))).toBe('Compose')
  })
})
