import { render } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { showPage } from '../test/pageshow'
import { usePageRestore } from './usePageRestore'

function Probe({ fn }: { fn: () => void }) {
  usePageRestore(fn)
  return null
}

describe('usePageRestore', () => {
  it('calls back on a pageshow from the back/forward cache only, until unmounted', () => {
    const fn = vi.fn()
    const { unmount } = render(<Probe fn={fn} />)
    showPage(false)
    expect(fn).not.toHaveBeenCalled()
    showPage(true)
    expect(fn).toHaveBeenCalledTimes(1)
    unmount()
    showPage(true)
    expect(fn).toHaveBeenCalledTimes(1)
  })
})
