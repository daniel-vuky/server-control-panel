import type { DiskInfo, ServerInfo } from '../../api/types.gen'
import { formatBytes } from '../../lib/format'

/** dash is a fact the server could not read yet ("" before Docker answered once). */
const dash = (v: string) => (v === '' ? '—' : v)

/** coresWord is the core count, "—" for 0 (neither /proc nor docker info answered). */
export const coresWord = (n: number) => (n > 0 ? String(n) : '—')

/** ramWord is the RAM size, "—" for 0 (neither /proc nor docker info answered). */
export const ramWord = (bytes: number) => (bytes > 0 ? formatBytes(bytes) : '—')

/**
 * SystemInfoCard is design 05's System info (spec decisions 10 and 11): the
 * operating system, kernel, cores, RAM and Docker version (Node and pm2 are
 * dropped: v1 manages compose projects only), then each disk against the
 * disk alert threshold. With the fallback disk row it says how to list every
 * disk.
 */
export default function SystemInfoCard({ info }: { info: ServerInfo }) {
  const docker = info.docker.state === 'not_installed' ? 'Not installed' : dash(info.docker.version)
  return (
    <section
      aria-labelledby="info-title"
      className="flex flex-col gap-3.5 rounded-[10px] border border-border bg-surface p-[18px]"
    >
      <h2 id="info-title" className="m-0 text-[15px] font-semibold">
        System info
      </h2>
      <dl className="m-0 grid grid-cols-[minmax(0,140px)_minmax(0,1fr)] gap-x-4 gap-y-2.5 text-[13px]">
        <dt className="text-muted">Operating system</dt>
        <dd className="m-0">{dash(info.os)}</dd>
        <dt className="text-muted">Kernel</dt>
        <dd className="m-0 font-mono break-all">{dash(info.kernel)}</dd>
        <dt className="text-muted">CPU</dt>
        <dd className="m-0">{info.cores > 0 ? `${info.cores} vCPU` : '—'}</dd>
        <dt className="text-muted">RAM</dt>
        <dd className="m-0">{ramWord(info.ramBytes)}</dd>
        <dt className="text-muted">Docker</dt>
        <dd className="m-0 font-mono">{docker}</dd>
      </dl>
      <div className="flex flex-col gap-2 pt-1">
        <span className="text-[13px] text-muted">Disk</span>
        {info.disks.length === 0 && <span className="font-mono text-[13px]">—</span>}
        {info.disks.map((d) => (
          <DiskRow key={d.mount} disk={d} threshold={info.thresholds.diskPercent} />
        ))}
        <span className="text-xs text-muted">Alert threshold {info.thresholds.diskPercent}%</span>
        {info.disksPartial && (
          <span className="text-xs text-muted">Re-run install.sh to list every disk.</span>
        )}
      </div>
    </section>
  )
}

/** DiskRow is one disk: its mount point, used / total and share, and a bar red above the threshold. */
function DiskRow({ disk, threshold }: { disk: DiskInfo; threshold: number }) {
  const used = disk.totalBytes > 0 ? (disk.usedBytes / disk.totalBytes) * 100 : 0
  const over = used > threshold
  const shown = Math.min(100, Math.round(used))
  return (
    <div data-testid={`disk-${disk.mount}`} className="flex flex-col gap-1.5">
      <div className="flex flex-wrap justify-between gap-x-3 font-mono text-[13px]">
        <span className="break-all">{disk.mount}</span>
        <span className={over ? 'text-danger' : undefined}>
          {formatBytes(disk.usedBytes)} / {formatBytes(disk.totalBytes)} · {shown}%
        </span>
      </div>
      <span aria-hidden="true" className="flex h-2 rounded bg-divider">
        <span
          data-bar=""
          className={`rounded ${over ? 'bg-danger' : 'bg-accent'}`}
          style={{ width: `${shown}%` }}
        />
      </span>
    </div>
  )
}
