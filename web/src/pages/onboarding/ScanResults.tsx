import { FolderSearch, TriangleAlert } from 'lucide-react'
import { useState } from 'react'
import type { ScanJob, ScanResult, ScanRow, TrackItem } from '../../api/types.gen'
import DatabaseCell from '../../components/DatabaseCell'
import MonitorOnlyTag from '../../components/MonitorOnlyTag'
import StatusBadge from '../../components/StatusBadge'
import { plural } from '../../lib/plural'

export const NAME_MAX = 80

/** notShown is "K already tracked · J ignored, not shown" with only the non-zero parts. */
function notShown(r: ScanResult): string {
  const parts = []
  if (r.alreadyTracked > 0) parts.push(`${r.alreadyTracked} already tracked`)
  if (r.ignored > 0) parts.push(`${r.ignored} ignored`)
  return parts.length ? `${parts.join(' · ')}, not shown` : ''
}

/** Summary is the line under the step 2 heading; `where` adds the path and depth. */
function Summary({ job, result, where }: { job: ScanJob; result: ScanResult; where: boolean }) {
  const hidden = notShown(result)
  const scanned = `${plural(result.foldersScanned, 'folder', 'folders')} scanned`
  return (
    <>
      <p className="text-muted">
        {where ? (
          <>
            In <span className="font-mono text-text-2">{job.path}</span> · depth {job.depth} ·{' '}
            {scanned}
          </>
        ) : (
          scanned
        )}
      </p>
      {hidden && <p className="text-[12.5px] text-muted">{hidden}</p>}
      {result.warnings.length > 0 && (
        <ul className="flex flex-col gap-1 text-[12.5px] text-warning">
          {result.warnings.map((w) => (
            <li key={w} className="whitespace-pre-line">
              {w}
            </li>
          ))}
        </ul>
      )}
    </>
  )
}

type Choice = { checked: boolean; name: string }

type Props = {
  job: ScanJob & { result: ScanResult }
  /** True while a request runs: Rescan and Add are disabled. */
  busy: boolean
  error: string
  onRescan(): void
  onAdd(projects: TrackItem[]): void
}

/** ScanResults is step 2: the found projects, to check, rename and add. */
export default function ScanResults({ job, busy, error, onRescan, onAdd }: Props) {
  const rows = job.result.rows
  const [choices, setChoices] = useState<Choice[]>(() =>
    rows.map((r) => ({ checked: true, name: r.defaultName })),
  )

  const update = (i: number, c: Partial<Choice>) =>
    setChoices((prev) => prev.map((p, j) => (j === i ? { ...p, ...c } : p)))

  const selected = choices.filter((c) => c.checked).length
  const blankName = choices.some((c) => c.checked && c.name.trim() === '')
  const canAdd = selected > 0 && !blankName && !busy

  const add = () => {
    if (!canAdd) return
    // The server checks the name of every row, checked or not: an unchecked
    // row with a blank name goes out with its default name.
    onAdd(
      rows.map((r, i) => {
        const c = choices[i]
        const name = c.name.trim()
        return { path: r.path, displayName: name || r.defaultName, track: c.checked }
      }),
    )
  }

  return (
    <section
      aria-labelledby="results-title"
      className="flex w-full max-w-[980px] flex-col rounded-[14px] border border-border bg-surface"
    >
      <div className="flex flex-col gap-1.5 px-6 pt-6 pb-[18px]">
        <h2 id="results-title" className="text-xl font-semibold tracking-tight">
          Found {plural(rows.length, 'project', 'projects')}
        </h2>
        <Summary job={job} result={job.result} where />
      </div>

      <div className="overflow-x-auto border-t border-border">
        <table className="w-full min-w-[820px] table-fixed border-collapse text-left">
          <colgroup>
            <col className="w-11" />
            <col className="w-[25%]" />
            <col className="w-[33%]" />
            <col className="w-[14%]" />
            <col />
          </colgroup>
          <thead>
            <tr className="border-b border-border text-xs tracking-wider text-muted uppercase">
              <th scope="col" className="w-11 py-2.5 pl-4 font-normal">
                <span className="sr-only">Track</span>
              </th>
              <th scope="col" className="px-3 py-2.5 font-normal">
                Display name
              </th>
              <th scope="col" className="px-3 py-2.5 font-normal">
                Path
              </th>
              <th scope="col" className="px-3 py-2.5 font-normal">
                Status
              </th>
              <th scope="col" className="py-2.5 pr-6 pl-3 font-normal">
                Database
              </th>
            </tr>
          </thead>
          <tbody>
            {rows.map((r, i) => (
              <Row key={r.path} row={r} choice={choices[i]} onChange={(c) => update(i, c)} />
            ))}
          </tbody>
        </table>
      </div>

      <div className="flex flex-wrap items-center gap-3 border-t border-border px-6 py-4">
        <button
          type="button"
          onClick={onRescan}
          disabled={busy}
          className="h-10 cursor-pointer rounded-lg border border-border-strong px-4 text-text disabled:cursor-default disabled:opacity-50"
        >
          Rescan
        </button>
        {error && (
          <p role="alert" className="text-[13px] text-danger">
            {error}
          </p>
        )}
        <span className="ml-auto text-[13px] text-muted">
          Selected{' '}
          <span className="font-mono text-text">
            {selected} / {rows.length}
          </span>
        </span>
        <button
          type="button"
          onClick={add}
          disabled={!canAdd}
          className="h-10 cursor-pointer rounded-lg bg-text px-5 font-semibold text-bg disabled:cursor-default disabled:opacity-50"
        >
          Add {plural(selected, 'project', 'projects')}
        </button>
      </div>
    </section>
  )
}

function Row({
  row,
  choice,
  onChange,
}: {
  row: ScanRow
  choice: Choice
  onChange(c: Partial<Choice>): void
}) {
  const invalid = choice.checked && choice.name.trim() === ''
  return (
    <tr
      className={`border-b border-border align-top last:border-b-0 ${choice.checked ? '' : 'opacity-55'}`}
    >
      <td className="py-3 pl-4">
        <span className="flex h-8 items-center justify-center">
          <input
            type="checkbox"
            checked={choice.checked}
            onChange={(e) => onChange({ checked: e.target.checked })}
            aria-label={`Track ${row.path}`}
            className="size-[18px] cursor-pointer accent-text"
          />
        </span>
      </td>
      <td className="px-3 py-3">
        <span className="flex items-center gap-2">
          <input
            type="text"
            value={choice.name}
            onChange={(e) => onChange({ name: e.target.value })}
            maxLength={NAME_MAX}
            spellCheck={false}
            aria-label={`Display name for ${row.path}`}
            aria-invalid={invalid ? true : undefined}
            className={`h-8 w-full min-w-0 rounded-md border bg-bg px-2 font-mono font-semibold text-text outline-none focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-accent ${
              invalid ? 'border-danger' : 'border-border-strong'
            }`}
          />
          {row.self && <MonitorOnlyTag />}
        </span>
      </td>
      <td className="px-3 py-3">
        <span className="block truncate pt-1.5 font-mono text-[12.5px] text-muted" title={row.path}>
          {row.path}
        </span>
        {row.warning && (
          <span className="mt-1 flex items-start gap-1.5 text-[12.5px] text-warning">
            <TriangleAlert
              aria-hidden="true"
              size={14}
              strokeWidth={2}
              className="mt-0.5 flex-none"
            />
            <span className="whitespace-pre-line">{row.warning}</span>
          </span>
        )}
      </td>
      <td className="px-3 py-3">
        <span className="flex h-8 items-center">
          <StatusBadge status={row.status} />
        </span>
      </td>
      <td className="py-3 pr-6 pl-3 text-[13px]">
        <span className="flex min-h-8 flex-col justify-center">
          <DatabaseCell databases={row.databases} />
        </span>
      </td>
    </tr>
  )
}

/** NothingFound is step 2 when the scan found no compose project. */
export function NothingFound({
  job,
  busy,
  onDeeper,
  onCheckPath,
}: {
  job: ScanJob & { result: ScanResult }
  busy: boolean
  onDeeper(): void
  onCheckPath(): void
}) {
  return (
    <section
      aria-labelledby="results-title"
      className="flex w-full max-w-[640px] flex-col gap-[18px] rounded-[14px] border border-border bg-surface p-8"
    >
      <FolderSearch aria-hidden="true" size={28} strokeWidth={1.6} className="text-muted" />
      <div className="flex flex-col gap-1.5">
        <h2 id="results-title" className="text-xl font-semibold tracking-tight">
          No compose project found in <span className="font-mono">{job.path}</span> at depth{' '}
          {job.depth}.
        </h2>
        <Summary job={job} result={job.result} where={false} />
      </div>
      <div className="flex flex-wrap items-center gap-3">
        {job.depth < 3 && (
          <button
            type="button"
            onClick={onDeeper}
            disabled={busy}
            className="h-10 cursor-pointer rounded-lg bg-text px-5 font-semibold text-bg disabled:cursor-default disabled:opacity-50"
          >
            Scan again at depth {job.depth + 1}
          </button>
        )}
        <button
          type="button"
          onClick={onCheckPath}
          className="h-10 cursor-pointer rounded-lg border border-border-strong px-4 text-text"
        >
          Check the path
        </button>
      </div>
    </section>
  )
}
