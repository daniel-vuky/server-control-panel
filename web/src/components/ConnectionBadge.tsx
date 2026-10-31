import { useLive } from '../app/live'

const pill = 'flex h-[30px] items-center gap-2 rounded-full border px-3 text-[12.5px]'
const dot = 'size-[7px] flex-none rounded-full'

/**
 * ConnectionBadge tells the owner whether the values on screen are live, per
 * designs 01 and 10. It shows nothing while the first connection opens.
 */
export default function ConnectionBadge({
  onReload = () => window.location.reload(),
}: {
  /** Reload the page; a test passes its own. */
  onReload?: () => void
}) {
  const { connection } = useLive()
  return (
    // The live region stays mounted so a change of the badge is announced.
    <div aria-live="polite" className="flex justify-end">
      {connection === 'open' && (
        <span className={`${pill} border-success-bg bg-success-soft text-success-text`}>
          <span aria-hidden="true" className={`${dot} bg-success`} />
          Realtime
        </span>
      )}
      {connection === 'lost' && (
        <span className={`${pill} border-warning-border bg-warning-bg text-warning-text`}>
          <span aria-hidden="true" className={`${dot} bg-warning`} />
          Connection lost, retrying…
        </span>
      )}
      {connection === 'expired' && (
        <span className={`${pill} border-error-border bg-error-bg pr-1 text-error-fg`}>
          <span aria-hidden="true" className={`${dot} bg-danger`} />
          Session expired
          <button
            type="button"
            onClick={onReload}
            className="ml-1 h-[22px] cursor-pointer rounded-full border border-error-strong bg-transparent px-2.5 font-semibold text-error-fg focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          >
            Reload
          </button>
        </span>
      )}
    </div>
  )
}
