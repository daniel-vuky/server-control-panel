import { useEffect, useRef } from 'react'
import { useSearchParams } from 'react-router'
import { connectWords } from '../api/drive'
import type { DriveStatus } from '../api/types.gen'
import { useToast } from '../components/Toast'

/**
 * useDriveReturn shows the toast for the page Google sent the browser back to
 * (?drive=connected|declined|error&code=...) and removes those parameters from
 * the URL. A connected return waits for the live state to name the account.
 * Other query parameters stay.
 */
export function useDriveReturn(drive: DriveStatus | null) {
  const [params, setParams] = useSearchParams()
  const toast = useToast()
  const seen = useRef(false)
  const outcome = params.get('drive')
  const code = params.get('code') ?? ''

  useEffect(() => {
    if (outcome === null) {
      seen.current = false
      return
    }
    if (seen.current) return
    if (outcome === 'connected') {
      if (drive === null) return
      toast.success(
        drive.email === '' ? 'Connected Google Drive' : `Connected Google Drive as ${drive.email}`,
      )
    } else if (outcome === 'declined') {
      toast.error('Google Drive was not connected.')
    } else if (outcome === 'error') {
      const words = connectWords(code)
      toast.error(
        'Google Drive was not connected.',
        [words, code && `(${code})`].filter(Boolean).join(' '),
      )
    } else {
      return
    }
    seen.current = true
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev)
        next.delete('drive')
        next.delete('code')
        return next
      },
      { replace: true },
    )
  }, [outcome, code, drive, toast, setParams])
}
