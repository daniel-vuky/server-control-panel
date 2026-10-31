import { useId } from 'react'
import type { Container } from '../../api/types.gen'
import ContainerStatus from '../../components/ContainerStatus'
import { containerTone } from '../../lib/containers'
import { formatCpu, formatMem } from '../../lib/format'

const ROW_TINT = {
  ok: '',
  failed: 'bg-[rgba(242,85,90,0.06)]',
  stopped: 'bg-[rgba(154,163,175,0.04)]',
} as const

const HEAD = 'px-1.5 py-2.5 font-normal first:pl-4 last:pr-4'
const CELL = 'px-1.5 py-[11px] align-top first:pl-4 last:pr-4'
/** The live columns (CPU, RAM, Restart) keep their width and align their digits to the right. */
const NUMBER = 'text-right whitespace-nowrap'

/**
 * ContainersTable is design 02's Containers card: "docker compose · R/T
 * running" (T counts the services with no container too) and one row per
 * container. With Docker down there is nothing to read, so it says so.
 */
export default function ContainersTable({
  containers,
  dockerDown,
}: {
  containers: Container[]
  dockerDown: boolean
}) {
  const headingId = useId()
  const running = containers.filter((c) => c.state === 'running').length

  return (
    <section aria-labelledby={headingId} className="rounded-[10px] border border-border bg-surface">
      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border px-4 py-3.5">
        <h2 id={headingId} className="m-0 text-[15px] font-semibold">
          Containers
        </h2>
        {!dockerDown && (
          <span className="text-[12.5px] text-muted">
            docker compose · {running}/{containers.length} running
          </span>
        )}
      </div>
      {dockerDown ? (
        <p className="m-0 px-4 py-5 text-[13px] text-muted">
          Docker is not responding, so containers can't be read.
        </p>
      ) : containers.length === 0 ? (
        <p className="m-0 px-4 py-5 text-[13px] text-muted">No containers yet.</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[880px] table-fixed border-collapse text-left">
            <colgroup>
              <col className="w-[20%]" />
              <col className="w-[13%]" />
              <col className="w-[20%]" />
              <col className="w-[15%]" />
              <col className="w-[10%]" />
              <col className="w-[10%]" />
              <col className="w-[12%]" />
            </colgroup>
            <thead>
              <tr className="border-b border-border text-xs tracking-[0.05em] text-muted uppercase">
                {['Name', 'Status', 'Image', 'Port', 'CPU', 'RAM', 'Restart'].map((h, i) => (
                  <th key={h} scope="col" className={i >= 4 ? `${HEAD} text-right` : HEAD}>
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody className="font-mono text-[13px] tabular-nums">
              {containers.map((c) => {
                const tone = containerTone(c)
                return (
                  <tr
                    key={`${c.service}|${c.name}`}
                    data-tone={tone}
                    className={`border-b border-divider last:border-b-0 ${ROW_TINT[tone]}`}
                  >
                    <td className={CELL}>
                      <span className="block break-all text-white">{c.name}</span>
                      {tone !== 'ok' && c.exitReason !== '' && (
                        <span
                          className={`mt-0.5 block font-sans text-xs ${
                            tone === 'failed' ? 'text-danger-text' : 'text-muted'
                          }`}
                        >
                          {c.exitReason}
                        </span>
                      )}
                    </td>
                    <td className={CELL}>
                      <ContainerStatus container={c} />
                    </td>
                    <td className={`${CELL} break-all text-text-2`}>{c.image || '—'}</td>
                    <td className={`${CELL} break-all text-text-2`}>
                      {c.ports.length > 0 ? c.ports.join(', ') : '—'}
                    </td>
                    <td className={`${CELL} ${NUMBER}`}>{formatCpu(c.cpuPercent, 1)}</td>
                    <td className={`${CELL} ${NUMBER}`}>{formatMem(c.memBytes)}</td>
                    <td className={`${CELL} ${NUMBER}`}>{c.restartCount}</td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}
