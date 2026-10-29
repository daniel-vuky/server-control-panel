import { describe, expect, it } from 'vitest'
import { plural } from './plural'

describe('plural', () => {
  it.each([
    [0, '0 projects'],
    [1, '1 project'],
    [2, '2 projects'],
  ])('%i -> %s', (n, want) => {
    expect(plural(n, 'project', 'projects')).toBe(want)
  })
})
