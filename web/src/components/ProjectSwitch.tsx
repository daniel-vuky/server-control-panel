import { disabledReason, isInFlight, type Status } from '../lib/status'

type Props = {
  name: string
  status: Status
  actions: string[]
  onStart(): void
  onStop(): void
  /** self marks the panel's own project, which is Monitor only. */
  self?: boolean
  /** dockerDown marks Docker as not answering. */
  dockerDown?: boolean
}

const ON: ReadonlySet<string> = new Set(['running', 'partial', 'starting', 'restarting'])

function label(status: Status, on: boolean): string {
  if (status === 'starting') return 'Starting'
  if (status === 'stopping') return 'Stopping'
  if (status === 'restarting') return 'Restarting'
  return on ? 'On' : 'Off'
}

/**
 * ProjectSwitch is the Start / Stop switch of design 01. It reads on for a
 * project that is up or coming up, and is locked when the action it would run
 * is not allowed, with the reason in its title.
 */
export default function ProjectSwitch({
  name,
  status,
  actions,
  onStart,
  onStop,
  self,
  dockerDown,
}: Props) {
  const on = ON.has(status)
  const busy = isInFlight(status)
  const needed = on ? 'stop' : 'start'
  const disabled = !actions.includes(needed)

  const reason = disabled ? disabledReason(status, self, dockerDown) : undefined

  // Design 01 colours by `on` first: a stopping switch is off, so it stays grey.
  const bg = on ? (busy ? 'bg-switch-busy' : 'bg-switch-on') : 'bg-switch-off'

  return (
    <span className="inline-flex items-center gap-2.5">
      <span className="text-[12.5px] text-text-2">{label(status, on)}</span>
      <button
        type="button"
        role="switch"
        aria-checked={on}
        aria-label={`${on ? 'Stop' : 'Start'} ${name}`}
        disabled={disabled}
        title={reason}
        onClick={on ? onStop : onStart}
        className={`relative h-[26px] w-11 flex-none cursor-pointer rounded-full border-0 p-0 transition-colors duration-200 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-50 ${bg}`}
      >
        <span
          aria-hidden="true"
          className={`absolute top-[3px] size-5 rounded-full bg-white transition-[left] duration-200 ${on ? 'left-[21px]' : 'left-[3px]'}`}
        />
      </button>
    </span>
  )
}
