import { Link } from 'react-router'
import MonitorOnlyTag from '../../components/MonitorOnlyTag'
import StatusBadge from '../../components/StatusBadge'
import { formatCpu, formatMem } from '../../lib/format'
import {
  Domain,
  ItemMenu,
  ItemSwitch,
  LastBackupLine,
  type ProjectItem,
  uptimeOrIssue,
} from './ProjectCard'
import { stackNote } from './stack'

const HEAD = 'px-3 py-2.5 font-normal'

/**
 * ProjectRows is the Dashboard's table view (D24): the same projects, words
 * and controls as the cards, one row each; CPU and RAM show the value only,
 * as design 01's table. The number columns have set
 * widths (table-layout fixed), so live values never move the columns.
 */
export default function ProjectRows({ items }: { items: ProjectItem[] }) {
  return (
    <section
      aria-label="Projects table"
      className="overflow-x-auto rounded-[10px] border border-border bg-surface"
    >
      <table className="w-full min-w-[1000px] table-fixed border-collapse text-left">
        <thead>
          <tr className="border-b border-border text-xs tracking-[0.05em] text-muted uppercase">
            <th scope="col" className="w-[18%] py-2.5 pr-3 pl-4 font-normal">
              Project
            </th>
            <th scope="col" className={`w-[118px] ${HEAD}`}>
              Status
            </th>
            <th scope="col" className={HEAD}>
              Domain
            </th>
            <th scope="col" className={`w-[84px] ${HEAD}`}>
              CPU
            </th>
            <th scope="col" className={`w-[96px] ${HEAD}`}>
              RAM
            </th>
            <th scope="col" className={`w-[140px] ${HEAD}`}>
              Uptime
            </th>
            <th scope="col" className={`w-[170px] ${HEAD}`}>
              Last backup
            </th>
            <th scope="col" className="w-[196px] py-2.5 pr-4 pl-3 text-right font-normal">
              Start / Stop
            </th>
          </tr>
        </thead>
        <tbody>
          {items.map((item) => (
            <Row key={item.project.id} item={item} />
          ))}
        </tbody>
      </table>
    </section>
  )
}

function Row({ item }: { item: ProjectItem }) {
  const p = item.project
  const third = uptimeOrIssue(p)
  return (
    <tr className="border-b border-divider last:border-b-0">
      <td className="py-2.5 pr-3 pl-4">
        <span className="flex min-w-0 flex-col gap-0.5">
          <span className="flex min-w-0 items-center gap-2">
            <Link
              to={`/projects/${p.id}`}
              className="truncate font-mono font-semibold text-white no-underline hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
            >
              {p.displayName}
            </Link>
            {p.self && <MonitorOnlyTag />}
          </span>
          <span className="truncate text-xs text-muted">{stackNote(p)}</span>
        </span>
      </td>
      <td className="px-3 py-2.5">
        <StatusBadge status={p.status} />
      </td>
      <td className="px-3 py-2.5">
        <Domain project={p} />
      </td>
      <td className="px-3 py-2.5">
        <span className="font-mono text-[13px] tabular-nums">{formatCpu(p.cpuPercent)}</span>
      </td>
      <td className="px-3 py-2.5">
        <span className="font-mono text-[13px] tabular-nums">{formatMem(p.memBytes)}</span>
      </td>
      <td className="px-3 py-2.5">
        {third.label === 'Issue' ? (
          <span className="text-[13px] leading-[1.3] break-words text-danger-text">
            <span className="sr-only">Issue: </span>
            {third.text}
          </span>
        ) : (
          <span className="font-mono text-[13px] tabular-nums">{third.text}</span>
        )}
      </td>
      <td className="px-3 py-2.5">
        <LastBackupLine latest={item.latest} />
      </td>
      <td className="py-2 pr-4 pl-3">
        <div className="flex items-center justify-end gap-2.5">
          <ItemSwitch item={item} />
          <ItemMenu item={item} />
        </div>
      </td>
    </tr>
  )
}
