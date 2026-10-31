import { useState } from 'react'
import type { ScanJob, ScanResult, TrackItem } from '../api/types.gen'
import { plural } from '../lib/plural'
import { type Choice, Row, Summary } from './onboarding/ScanResults'

type Props = {
  job: ScanJob & { result: ScanResult }
  /** True while a request runs: Add is disabled. */
  busy: boolean
  error: string
  onAdd(projects: TrackItem[]): void
  onBack(): void
}

/** NewTag is the "New" tag of design 10. */
function NewTag() {
  return (
    <span className="flex-none rounded-[4px] bg-success-bg px-[7px] py-px text-[11.5px] text-success-text">
      New
    </span>
  )
}

/** summaryLine is "Rescanned N workspaces · X new · Y not found · Z ignored". */
function summaryLine(r: ScanResult): string {
  return `Rescanned ${plural(r.workspaces.length, 'workspace', 'workspaces')} · ${r.new} new · ${r.notFound} not found · ${r.ignoredFound} ignored`
}

/**
 * RescanResults is the results step of a rescan: the New projects, checked and
 * tagged, and the ignored ones behind "Show ignored (N)", unchecked. It has no
 * Rescan button: the page itself was reached by one.
 */
export default function RescanResults({ job, busy, error, onAdd, onBack }: Props) {
  const result = job.result
  const fresh = result.rows.filter((r) => !r.ignored)
  const ignored = result.rows.filter((r) => r.ignored)

  const [showIgnored, setShowIgnored] = useState(false)
  const [choices, setChoices] = useState<Record<string, Choice>>(() =>
    Object.fromEntries(
      result.rows.map((r) => [r.path, { checked: !r.ignored, name: r.defaultName }]),
    ),
  )

  // A hidden ignored row is neither counted nor sent, even if it was checked.
  const shown = showIgnored ? [...fresh, ...ignored] : fresh
  const update = (path: string, c: Partial<Choice>) =>
    setChoices((prev) => ({ ...prev, [path]: { ...prev[path], ...c } }))

  const picked = shown.filter((r) => choices[r.path].checked)
  const blankName = picked.some((r) => choices[r.path].name.trim() === '')
  const canAdd = picked.length > 0 && !blankName && !busy

  const add = () => {
    if (!canAdd) return
    // Only the rows to track go out: the server keeps the other New rows as
    // ignored and the other Ignored rows as they are.
    onAdd(
      picked.map((r) => ({
        path: r.path,
        displayName: choices[r.path].name.trim(),
        track: true,
      })),
    )
  }

  return (
    <section
      aria-labelledby="results-title"
      className="flex w-full max-w-[980px] flex-col rounded-[14px] border border-border bg-surface"
    >
      <div className="flex flex-col gap-1.5 px-6 pt-6 pb-[18px]">
        <h1 id="results-title" className="m-0 text-xl font-semibold tracking-tight">
          Rescan results
        </h1>
        <p className="text-text-2">{summaryLine(result)}</p>
        <Summary job={job} result={result} where={false} />
        {ignored.length > 0 && (
          <label className="mt-1 flex w-fit cursor-pointer items-center gap-2 text-[13px] text-text-2">
            <input
              type="checkbox"
              checked={showIgnored}
              onChange={(e) => setShowIgnored(e.target.checked)}
              className="size-4 cursor-pointer accent-text"
            />
            Show ignored ({ignored.length})
          </label>
        )}
      </div>

      {shown.length > 0 && (
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
              {shown.map((r) => (
                <Row
                  key={r.path}
                  row={r}
                  choice={choices[r.path]}
                  onChange={(c) => update(r.path, c)}
                  tag={r.new ? <NewTag /> : undefined}
                />
              ))}
            </tbody>
          </table>
        </div>
      )}

      <div className="flex flex-wrap items-center gap-3 border-t border-border px-6 py-4">
        <button
          type="button"
          onClick={onBack}
          className="h-10 cursor-pointer rounded-lg border border-border-strong px-4 text-text"
        >
          Back to Dashboard
        </button>
        {error && (
          <p role="alert" className="text-[13px] text-danger">
            {error}
          </p>
        )}
        {shown.length > 0 && (
          <>
            <span className="ml-auto text-[13px] text-muted">
              Selected{' '}
              <span className="font-mono text-text">
                {picked.length} / {shown.length}
              </span>
            </span>
            <button
              type="button"
              onClick={add}
              disabled={!canAdd}
              className="h-10 cursor-pointer rounded-lg bg-text px-5 font-semibold text-bg disabled:cursor-default disabled:opacity-50"
            >
              Add {plural(picked.length, 'project', 'projects')}
            </button>
          </>
        )}
      </div>
    </section>
  )
}
