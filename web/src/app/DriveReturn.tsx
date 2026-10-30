import { useDriveReturn } from '../lib/useDriveReturn'
import { useLive } from './live'

/**
 * DriveReturn mounts once under the live provider and shows the toast for the
 * page Google sent the browser back to, whichever page that is.
 */
export default function DriveReturn() {
  useDriveReturn(useLive().drive)
  return null
}
