import { type ReactNode, useCallback, useEffect, useRef, useState } from 'react'
import { useLocation, useNavigate, useSearchParams } from 'react-router'
import { ApiError, isFieldError } from '../../api/client'
import { getScan, isScanBusy, startScan, trackScan, watchScan } from '../../api/scans'
import type { ScanJob, ScanProgress, ScanResult, TrackItem } from '../../api/types.gen'
import { listWorkspaces } from '../../api/workspaces'
import { LiveDim, useLive } from '../../app/live'
import { BrandRow } from '../../components/PageHeader'
import { useToast } from '../../components/Toast'
import { plural } from '../../lib/plural'
import { useRead } from '../../lib/useRead'
import DoneStep from './DoneStep'
import DriveStep from './DriveStep'
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

/** The steps of adding a workspace, and the four of the first run (design 03). */
const ADD_STEPS = ['Workspace', 'Scan results'] as const
const FIRST_RUN_STEPS = ['Workspace', 'Scan results', 'Google Drive', 'Done'] as const

/** SetupStep is the step the URL names: ?step=drive|done&added=N with N a positive integer. */
type SetupStep = { step: 'drive' | 'done'; added: number }

/** setupStep reads the URL's step; a missing or bad one gives null, so the form shows. */
function setupStep(params: URLSearchParams): SetupStep | null {
  const step = params.get('step')
  const added = params.get('added') ?? ''
  if ((step !== 'drive' && step !== 'done') || !/^[1-9]\d{0,8}$/.test(added)) return null
  return { step, added: Number(added) }
}

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

/**
 * OnboardingPage is /onboarding: design 03. The first run (no workspace yet)
 * has four steps, Workspace, Scan results, Google Drive and Done; adding a
 * workspace from Settings has the first two. The URL decides first: ?step=
 * names the Drive and Done steps and means the first run whatever the
 * workspace count (after Add a workspace exists, so a return from Google or a
 * reload of Done cannot ask the list). Only a bare /onboarding reads the list.
 */
export default function OnboardingPage() {
  const navigate = useNavigate()
  const toast = useToast()
  const { drive } = useLive()
  const [params, setParams] = useSearchParams()
  const urlStep = setupStep(params)
  // Until the list answers, and when it fails, the page is the two-step Add a workspace.
  const workspaces = useRead(listWorkspaces, 'workspaces').data
  const firstRun = urlStep !== null || workspaces?.length === 0
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
      if (firstRun && res.added > 0) {
        // The Drive step is skipped when Drive is ready: connected with a folder.
        const ready = drive?.state === 'connected' && drive.folder !== null
        setParams({ step: ready ? 'done' : 'drive', added: String(res.added) }, { replace: true })
        setPhase({ kind: 'form' })
        setAdding(false)
        return
      }
      toast.show(`Added ${plural(res.added, 'project', 'projects')}`)
      navigate('/')
    } catch (err) {
      setTrackError(message(err))
      setAdding(false)
    }
  }

  // Rescan goes back to the Workspace step, path and depth kept; Scan repeats it.
  const rescan = (job: DoneJob) => {
    setPath(job.path)
    setDepth(job.depth)
    setErrors({})
    setFormError('')
    setTrackError('')
    setPhase({ kind: 'form' })
    setFocusPath((n) => n + 1)
  }

  const step = urlStep
    ? urlStep.step === 'drive'
      ? 2
      : 3
    : phase.kind === 'results' || phase.kind === 'empty'
      ? 1
      : 0

  let body: ReactNode
  if (urlStep?.step === 'drive') {
    body = (
      <DriveStep
        added={urlStep.added}
        onFinish={() =>
          setParams({ step: 'done', added: String(urlStep.added) }, { replace: true })
        }
      />
    )
  } else if (urlStep?.step === 'done') {
    body = <DoneStep added={urlStep.added} />
  } else if (phase.kind === 'results') {
    const job = phase.job
    body = (
      <ScanResults
        key={job.id}
        job={job}
        busy={starting || adding}
        error={trackError}
        onRescan={() => rescan(job)}
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
      <BrandRow label={firstRun ? 'First-time setup' : 'Add a workspace'}>
        <Stepper steps={firstRun ? FIRST_RUN_STEPS : ADD_STEPS} current={step} />
      </BrandRow>
      <LiveDim className="flex w-full flex-col items-center gap-8">{body}</LiveDim>
    </div>
  )
}

function Stepper({ steps, current }: { steps: readonly string[]; current: number }) {
  return (
    <ol aria-label="Setup steps" className="flex flex-wrap items-center gap-2">
      {steps.map((label, i) => {
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
            {i < steps.length - 1 && (
              <span aria-hidden="true" className="h-px w-7 bg-border-strong" />
            )}
          </li>
        )
      })}
    </ol>
  )
}
