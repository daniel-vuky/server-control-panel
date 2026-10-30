import { Folder, Search } from 'lucide-react'
import type { FormEvent, ReactNode, Ref } from 'react'
import FieldMessage from '../../components/FieldMessage'

export const DEPTHS = [1, 2, 3] as const

export type FieldErrors = { path?: string; depth?: string }

type Props = {
  path: string
  depth: number
  errors: FieldErrors
  /** True while a scan starts or runs: the inputs and Scan are disabled. */
  locked: boolean
  onPath(path: string): void
  onDepth(depth: number): void
  onScan(): void
  pathRef?: Ref<HTMLInputElement>
  /** The progress box or the error panel, shown above the Scan button. */
  children?: ReactNode
}

const depthLabel = (d: number) => (d === 1 ? '1 level' : `${d} levels`)

/** WorkspaceStep is step 1: the root folder path, the depth and Scan. */
export default function WorkspaceStep({
  path,
  depth,
  errors,
  locked,
  onPath,
  onDepth,
  onScan,
  pathRef,
  children,
}: Props) {
  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (!locked) onScan()
  }

  return (
    <section
      aria-labelledby="workspace-title"
      className="flex w-full max-w-[640px] flex-col gap-[22px] rounded-[14px] border border-border bg-surface p-8"
    >
      <div className="flex flex-col gap-2">
        <h2 id="workspace-title" className="text-[22px] font-semibold tracking-tight">
          Add project folder
        </h2>
        <p className="leading-relaxed text-muted">
          Enter the root folder containing projects on the server. The panel only reads inside this
          folder and never moves or modifies any files.
        </p>
      </div>

      <form className="flex flex-col gap-[22px]" onSubmit={submit} noValidate>
        <div className="flex flex-col gap-2">
          <label htmlFor="workspace-path" className="text-[13px] text-text-2">
            Root folder path
          </label>
          <span
            className={`flex h-11 items-center gap-2 rounded-lg border bg-bg px-3 focus-within:outline-2 focus-within:outline-offset-2 focus-within:outline-accent ${
              errors.path ? 'border-danger' : 'border-border-strong'
            } ${locked ? 'opacity-60' : ''}`}
          >
            <Folder
              aria-hidden="true"
              size={16}
              strokeWidth={1.8}
              className="flex-none text-muted"
            />
            <input
              id="workspace-path"
              ref={pathRef}
              type="text"
              value={path}
              onChange={(e) => onPath(e.target.value)}
              disabled={locked}
              placeholder="/home/deploy/projects"
              spellCheck={false}
              autoComplete="off"
              aria-invalid={errors.path ? true : undefined}
              aria-describedby={errors.path ? 'workspace-path-error' : undefined}
              className="min-w-0 flex-1 bg-transparent font-mono text-sm text-text outline-none placeholder:text-muted/60"
            />
          </span>
          {errors.path && <FieldMessage id="workspace-path-error" message={errors.path} />}
        </div>

        <fieldset className="flex flex-col gap-2" aria-describedby="depth-hint">
          <legend className="mb-2 text-[13px] text-text-2">Scan depth</legend>
          <div
            className={`flex w-fit rounded-lg border bg-bg p-[3px] ${
              errors.depth ? 'border-danger' : 'border-border-strong'
            }`}
          >
            {DEPTHS.map((d) => (
              <label key={d} className="relative">
                <input
                  type="radio"
                  name="depth"
                  value={d}
                  checked={depth === d}
                  onChange={() => onDepth(d)}
                  disabled={locked}
                  aria-invalid={errors.depth ? true : undefined}
                  className="peer absolute inset-0 cursor-pointer opacity-0 disabled:cursor-default"
                />
                <span className="flex h-8 items-center rounded-md px-4 text-[13px] text-muted peer-checked:bg-border-strong peer-checked:text-white peer-focus-visible:outline-2 peer-focus-visible:outline-accent peer-disabled:opacity-60">
                  {depthLabel(d)}
                </span>
              </label>
            ))}
          </div>
          {errors.depth && <FieldMessage id="workspace-depth-error" message={errors.depth} />}
          <span id="depth-hint" className="text-[12.5px] text-muted">
            1 level: each subfolder is one project. Increase it if projects are nested deeper, e.g.{' '}
            <span className="font-mono">clients/acme/web</span>.
          </span>
        </fieldset>

        {children}

        <div className="flex flex-wrap items-center gap-3">
          <button
            type="submit"
            disabled={locked}
            className="flex h-[42px] cursor-pointer items-center gap-2 rounded-lg bg-text px-[22px] font-semibold text-bg disabled:cursor-default disabled:opacity-50"
          >
            <Search aria-hidden="true" size={16} strokeWidth={2} />
            Scan
          </button>
          <span className="text-[12.5px] text-muted">Detects: docker compose</span>
        </div>
      </form>
    </section>
  )
}
