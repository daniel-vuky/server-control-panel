import { TriangleAlert } from 'lucide-react'
import { useLive } from '../app/live'

/** DockerBanner tells the owner Docker does not answer, with a way to ask again. */
export default function DockerBanner() {
  const { dockerUp, checking, recheckDocker } = useLive()
  if (dockerUp !== false) return null
  return (
    <div
      role="alert"
      className="mb-[22px] flex flex-wrap items-center gap-3 rounded-lg border border-error-border bg-error-bg px-4 py-3 text-error-fg"
    >
      <TriangleAlert
        aria-hidden="true"
        size={18}
        strokeWidth={2}
        className="flex-none text-danger"
      />
      <span className="font-semibold">Docker daemon is not responding.</span>
      <span className="text-error-text">
        Start/Stop for Docker projects is locked. Check{' '}
        <span className="font-mono">systemctl status docker</span> on the server.
      </span>
      <button
        type="button"
        disabled={checking}
        onClick={() => void recheckDocker()}
        className="ml-auto h-8 cursor-pointer rounded-md border border-error-strong bg-transparent px-3 text-error-fg focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-50"
      >
        Retry
      </button>
    </div>
  )
}
