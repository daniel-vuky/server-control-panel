import { useState } from 'react'
import { useNavigate } from 'react-router'
import { ApiError } from '../api/client'
import { isScanBusy } from '../api/scans'
import { startRescan } from '../api/workspaces'
import { useToast } from '../components/Toast'

/**
 * useRescan starts a rescan of one workspace, or of every workspace without an
 * id, and opens it. A 409 with a running scan attaches to it: a rescan has its
 * page, a first scan is shown by onboarding. Any other failure is an error toast.
 */
export function useRescan() {
  const navigate = useNavigate()
  const toast = useToast()
  const [starting, setStarting] = useState(false)

  const rescan = async (workspaceId = 0) => {
    setStarting(true)
    try {
      const { id } = await startRescan(workspaceId)
      navigate(`/rescan/${id}`)
    } catch (err) {
      if (err instanceof ApiError && err.status === 409 && isScanBusy(err.body)) {
        if (err.body.rescan) navigate(`/rescan/${err.body.id}`)
        else navigate('/onboarding', { state: { scanId: err.body.id } })
        return
      }
      const message = err instanceof Error && err.message ? err.message : 'Something went wrong.'
      toast.error('Could not start the rescan', message)
    } finally {
      setStarting(false)
    }
  }

  return { starting, rescan }
}
