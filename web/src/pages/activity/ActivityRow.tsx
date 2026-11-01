import { Power, TriangleAlert } from 'lucide-react'
import { Link } from 'react-router'
import type { ActivityEntry } from '../../api/types.gen'
import CloudUploadIcon from '../../components/CloudUploadIcon'
import { clockOf } from './filters'
import { actorOf, detailOf, pillOf, type Tone, verbOf } from './words'

/** PILL are the result pill's colours, the tints of design 06's RES. */
const PILL: Record<Tone, string> = {
  ok: 'bg-[rgba(63,207,142,0.12)] text-success',
  fail: 'bg-[rgba(242,85,90,0.14)] text-danger-text',
  run: 'bg-[rgba(76,155,245,0.14)] text-accent-text',
  sent: 'bg-[rgba(229,185,59,0.13)] text-warning-text',
  info: 'bg-[rgba(154,163,175,0.12)] text-text-2',
}

/** ICON are the kind icon boxes of design 06's KIND: red alert, blue backup, grey action. */
const ICON: Record<string, string> = {
  alert: 'bg-[rgba(242,85,90,0.14)] text-danger-text',
  backup: 'bg-[rgba(76,155,245,0.12)] text-accent-text',
  action: 'bg-nav-active text-text-2',
}

const KindIcon = ({ kind }: { kind: string }) =>
  kind === 'alert' ? (
    <TriangleAlert aria-hidden="true" size={15} strokeWidth={2} />
  ) : kind === 'backup' ? (
    <CloudUploadIcon size={15} strokeWidth={1.9} />
  ) : (
    <Power aria-hidden="true" size={15} strokeWidth={1.9} />
  )

/**
 * ActivityRow is one record of design 06: time, kind icon, "actor verb
 * target" (the target links to the project while it is tracked), the detail
 * line and the result pill. An alert row has the faint red wash.
 */
export default function ActivityRow({ entry: e, first }: { entry: ActivityEntry; first: boolean }) {
  const pill = pillOf(e)
  const detail = detailOf(e)
  const kind = Object.hasOwn(ICON, e.kind) ? e.kind : 'action'
  return (
    <li
      className={`grid grid-cols-[56px_28px_minmax(0,1fr)_auto] items-start gap-3.5 px-4 py-3.5 ${first ? '' : 'border-t border-divider'} ${kind === 'alert' ? 'bg-[rgba(242,85,90,0.04)]' : ''}`}
    >
      <span className="pt-0.5 font-mono text-[13px] text-muted">{clockOf(e.at)}</span>
      <span
        aria-hidden="true"
        className={`flex size-7 items-center justify-center rounded-[7px] ${ICON[kind]}`}
      >
        <KindIcon kind={kind} />
      </span>
      <div className="flex min-w-0 flex-col gap-1">
        <span className="leading-[1.45] [overflow-wrap:anywhere]">
          <span className="font-semibold">{actorOf(e)}</span>{' '}
          <span className="text-text-2">{verbOf(e)}</span>{' '}
          {e.target !== '' &&
            (e.projectId !== null && e.projectTracked ? (
              <Link
                to={`/projects/${e.projectId}`}
                className="font-mono text-white no-underline hover:underline"
              >
                {e.target}
              </Link>
            ) : (
              <span className="font-mono text-white">{e.target}</span>
            ))}
        </span>
        {detail !== '' && (
          <span
            className={`text-[12.5px] [overflow-wrap:anywhere] ${pill.tone === 'fail' ? 'text-danger-text' : 'text-muted'} ${kind === 'action' ? '' : 'font-mono'}`}
          >
            {detail}
          </span>
        )}
      </div>
      <span
        className={`self-start justify-self-end rounded-full px-[9px] py-[3px] text-xs font-medium whitespace-nowrap ${PILL[pill.tone]}`}
      >
        {pill.label}
      </span>
    </li>
  )
}
