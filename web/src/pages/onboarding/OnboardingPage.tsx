import { type ReactNode, useCallback, useEffect, useRef, useState } from 'react'
import { useLocation, useNavigate } from 'react-router'
import { ApiError, isFieldError } from '../../api/client'
import { getScan, isScanBusy, startScan, trackScan, watchScan } from '../../api/scans'
import type { ScanJob, ScanProgress, ScanResult, TrackItem } from '../../api/types.gen'
import { LiveDim } from '../../app/live'
import { BrandRow } from '../../components/PageHeader'
import { useToast } from '../../components/Toast'
import ScanFailed from './ScanFailed'
import ScanProgressView from './ScanProgressView'
import ScanResults, { NothingFound } from './ScanResults'
import WorkspaceStep, { DEPTHS, type FieldErrors } from './WorkspaceStep'

type DoneJob = ScanJob & { result: ScanResult }

/**
 * The page's state machine: form → scanning(id) → results | empty | failed.
 * A scan's path and depth ride along so Rescan and Scan again can repeat it.
 */
type Phase =
  | { kind: 'form' }
  | { kind: 'scanning'; id: number; path: string; depth: number }
  | { kind: 'results'; job: DoneJob }
  | { kind: 'empty'; job: DoneJob }
  | { kind: 'failed'; path: string; depth: number; error: string }

const STEPS = ['Workspace', 'Scan results'] as const

const NOT_FINISHED = 'The scan did not finish.'

/** handedOffScan is the scan id the Dashboard or the rescan page passed in the router state. */
function handedOffScan(state: unknown): number | null {
  const id = (state as { scanId?: unknown } | null)?.scanId
  return typeof id === 'number' && Number.isInteger(id) && id > 0 ? id : null
}

const message = (err: unknown) =>
  err instanceof Error && err.message ? err.message : 'Something went wrong. Try again.'

const emptyProgress: ScanProgress = {
  examined: 0,
  total: 0,
  current: '',
  workspace: 0,
  workspaces: 0,
}

/** OnboardingPage is /onboarding: design 03 steps 1–2. */
export default function OnboardingPage() {
  const navigate = useNavigate()
  const toast = useToast()
  // The Dashboard sends the id of a first scan that already runs.
  const location = useLocation()
  const [handedOff] = useState(() => handedOffScan(location.state))

  const [path, setPath] = useState('')
  const [depth, setDepth] = useState(1)
  const [errors, setErrors] = useState<FieldErrors>({})
  const [formError, setFormError] = useState('')
  const [phase, setPhase] = useState<Phase>(() =>
    handedOff === null ? { kind: 'form' } : { kind: 'scanning', id: handedOff, path: '', depth: 1 },
  )
  const [progress, setProgress] = useState<ScanProgress>(emptyProgress)
  const [starting, setStarting] = useState(false)
  const [adding, setAdding] = useState(false)
  const [trackError, setTrackError] = useState('')
  const [focusPath, setFocusPath] = useState(0)
  const pathRef = useRef<HTMLInputElement>(null)

  const scan = useCallback(
    async (p: string, d: number) => {
      setPath(p)
      setDepth(d)
      setErrors({})
      setFormError('')
      setTrackError('')
      setStarting(true)
      try {
        const { id } = await startScan({ path: p, depth: d })
        setProgress(emptyProgress)
        setPhase({ kind: 'scanning', id, path: p, depth: d })
      } catch (err) {
        if (err instanceof ApiError && err.status === 409 && isScanBusy(err.body)) {
          if (err.body.rescan) {
            // A rescan runs: it has its own page.
            navigate(`/rescan/${err.body.id}`)
            return
          }
          // Another scan runs: attach to it, as if this tab had started it.
          setPath(err.body.path)
          setDepth(err.body.depth)
          setProgress(emptyProgress)
          setPhase({
            kind: 'scanning',
            id: err.body.id,
            path: err.body.path,
            depth: err.body.depth,
          })
        } else if (err instanceof ApiError && err.status === 422 && isFieldError(err.body)) {
          const field = err.body.field === 'depth' ? 'depth' : 'path'
          setErrors({ [field]: err.body.error })
          setPhase({ kind: 'form' })
        } else {
          setFormError(message(err))
          setPhase({ kind: 'form' })
        }
      } finally {
        setStarting(false)
      }
    },
    [navigate],
  )

  const scanId = phase.kind === 'scanning' ? phase.id : null

  useEffect(() => {
    if (scanId === null) return
    const ctrl = new AbortController()
    const fail = (error: string) =>
      setPhase((ph) =>
        ph.kind === 'scanning' && ph.id === scanId
          ? { kind: 'failed', path: ph.path, depth: ph.depth, error }
          : ph,
      )
    const stop = watchScan(scanId, {
      progress: setProgress,
      end: () => {
        getScan(scanId, ctrl.signal)
          .then((job) => {
            if (ctrl.signal.aborted) return
            if (job.state === 'done' && job.result) {
              const done = job as DoneJob
              setPhase({ kind: done.result.rows.length > 0 ? 'results' : 'empty', job: done })
            } else {
              setPhase({
                kind: 'failed',
                path: job.path,
                depth: job.depth,
                error: job.error || NOT_FINISHED,
              })
            }
          })
          .catch((err) => {
            if (!ctrl.signal.aborted) fail(message(err))
          })
      },
    })
    return () => {
      stop()
      ctrl.abort()
    }
  }, [scanId])

  // A handed-off scan starts without a path or depth: read them from the job.
  useEffect(() => {
    if (handedOff === null) return
    const ctrl = new AbortController()
    getScan(handedOff, ctrl.signal)
      .then((j) => {
        setPath(j.path)
        setDepth(j.depth)
        setPhase((ph) =>
          ph.kind === 'scanning' && ph.id === j.id ? { ...ph, path: j.path, depth: j.depth } : ph,
        )
      })
      .catch(() => {
        // The watch reports a job that cannot be read.
      })
    return () => ctrl.abort()
  }, [handedOff])

  useEffect(() => {
    if (focusPath > 0) pathRef.current?.focus()
  }, [focusPath])

  const checkPath = () => {
    setPhase({ kind: 'form' })
    setFocusPath((n) => n + 1)
  }

  const add = async (job: DoneJob, projects: TrackItem[]) => {
    setAdding(true)
    setTrackError('')
    try {
      const res = await trackScan(job.id, { projects })
      toast.show(res.added === 1 ? '1 project added' : `${res.added} projects added`)
      navigate('/')
    } catch (err) {
      setTrackError(message(err))
      setAdding(false)
    }
  }

  const step = phase.kind === 'results' || phase.kind === 'empty' ? 1 : 0

  let body: ReactNode
  if (phase.kind === 'results') {
    const job = phase.job
    body = (
      <ScanResults
        key={job.id}
        job={job}
        busy={starting || adding}
        error={trackError}
        onRescan={() => scan(job.path, job.depth)}
        onAdd={(projects) => add(job, projects)}
      />
    )
  } else if (phase.kind === 'empty') {
    const job = phase.job
    body = (
      <NothingFound
        job={job}
        busy={starting}
        onDeeper={() => scan(job.path, Math.min(job.depth + 1, DEPTHS.length))}
        onCheckPath={checkPath}
      />
    )
  } else {
    const scanning = phase.kind === 'scanning'
    // Editing the path or depth after a failed scan dismisses the failure, so
    // its Scan again cannot repeat the old values; Scan uses the new ones.
    const dismissFailed = () => {
      if (phase.kind === 'failed') setPhase({ kind: 'form' })
    }
    body = (
      <WorkspaceStep
        path={path}
        depth={depth}
        errors={errors}
        locked={scanning || starting}
        onPath={(p) => {
          setPath(p)
          dismissFailed()
          if (errors.path) setErrors((e) => ({ ...e, path: undefined }))
        }}
        onDepth={(d) => {
          setDepth(d)
          dismissFailed()
          if (errors.depth) setErrors((e) => ({ ...e, depth: undefined }))
        }}
        onScan={() => scan(path.trim(), depth)}
        pathRef={pathRef}
      >
        {scanning && <ScanProgressView progress={progress} />}
        {phase.kind === 'failed' && (
          <ScanFailed
            error={phase.error}
            busy={starting}
            onRetry={() => scan(phase.path, phase.depth)}
          />
        )}
        {formError && (
          <p role="alert" className="text-[13px] text-danger">
            {formError}
          </p>
        )}
      </WorkspaceStep>
    )
  }

  return (
    <div className="flex flex-col items-center gap-8">
      <BrandRow label="Add a workspace">
        <Stepper current={step} />
      </BrandRow>
      <LiveDim className="flex w-full flex-col items-center gap-8">{body}</LiveDim>
    </div>
  )
}

function Stepper({ current }: { current: number }) {
  return (
    <ol aria-label="Setup steps" className="flex flex-wrap items-center gap-2">
      {STEPS.map((label, i) => {
        const done = i < current
        const cur = i === current
        return (
          <li
            key={label}
            aria-current={cur ? 'step' : undefined}
            className="flex items-center gap-2"
          >
            <span
              aria-hidden="true"
              className={`flex size-6 items-center justify-center rounded-full border font-mono text-xs font-semibold ${
                cur
                  ? 'border-text bg-text text-bg'
                  : done
                    ? 'border-success-bg bg-success-bg text-success'
                    : 'border-border-strong text-muted'
              }`}
            >
              {done ? '✓' : i + 1}
            </span>
            <span className={`text-[13px] ${cur ? 'font-semibold text-white' : 'text-muted'}`}>
              {label}
            </span>
            {i < STEPS.length - 1 && (
              <span aria-hidden="true" className="h-px w-7 bg-border-strong" />
            )}
          </li>
        )
      })}
    </ol>
  )
}
