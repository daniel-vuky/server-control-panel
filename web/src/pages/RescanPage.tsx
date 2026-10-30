import { type ReactNode, useCallback, useEffect, useState } from 'react'
import { useLocation, useNavigate, useParams } from 'react-router'
import { ApiError } from '../api/client'
import { getScan, isScanBusy, trackScan, watchScan } from '../api/scans'
import type { ScanJob, ScanProgress, ScanResult, TrackItem } from '../api/types.gen'
import { startRescan } from '../api/workspaces'
import { useToast } from '../components/Toast'
import { plural } from '../lib/plural'
import ScanFailed from './onboarding/ScanFailed'
import ScanProgressView from './onboarding/ScanProgressView'
import RescanResults from './RescanResults'

type DoneJob = ScanJob & { result: ScanResult }

type Phase =
  | { kind: 'scanning' }
  | { kind: 'results'; job: DoneJob }
  | { kind: 'failed'; error: string; workspaceId: number }
  // The job is handled elsewhere: a toast and back, or onboarding.
  | { kind: 'leaving' }

const NOT_FINISHED = 'The rescan did not finish.'

const emptyProgress: ScanProgress = {
  examined: 0,
  total: 0,
  current: '',
  workspace: 0,
  workspaces: 0,
}

const message = (err: unknown) =>
  err instanceof Error && err.message ? err.message : 'Something went wrong. Try again.'

/** retryScope is the workspace to rescan again: the job's only one, else all (0). */
const retryScope = (job: ScanJob | null) =>
  job?.workspaceIds.length === 1 ? job.workspaceIds[0] : 0

/** RescanPage is /rescan/:id: the progress and results of a rescan (not in the nav). */
export default function RescanPage() {
  const { id } = useParams()
  const scanId = Number(id)
  // Keyed by the job: a retry is a new job on a fresh page.
  return <Rescan key={id} id={scanId} />
}

function Rescan({ id }: { id: number }) {
  const navigate = useNavigate()
  const location = useLocation()
  const toast = useToast()

  const [phase, setPhase] = useState<Phase>({ kind: 'scanning' })
  const [progress, setProgress] = useState<ScanProgress>(emptyProgress)
  const [starting, setStarting] = useState(false)
  const [adding, setAdding] = useState(false)
  const [trackError, setTrackError] = useState('')

  // The first entry of the history has nothing behind it, and neither has a
  // page a retry replaced when that one was the first.
  const firstEntry =
    location.key === 'default' || (location.state as { first?: boolean } | null)?.first === true

  const toOnboarding = useCallback(
    () => navigate('/onboarding', { replace: true, state: { scanId: id } }),
    [navigate, id],
  )

  useEffect(() => {
    if (!Number.isInteger(id) || id <= 0) {
      setPhase({ kind: 'failed', error: NOT_FINISHED, workspaceId: 0 })
      return
    }
    const ctrl = new AbortController()

    // A first scan has no page here: onboarding shows it. The check does not
    // wait for the job to end.
    getScan(id, ctrl.signal)
      .then((job) => {
        if (!ctrl.signal.aborted && !job.rescan) toOnboarding()
      })
      .catch(() => {
        // The watch reports a job that cannot be read.
      })

    const settle = (job: ScanJob) => {
      if (!job.rescan) {
        toOnboarding()
        setPhase({ kind: 'leaving' })
        return
      }
      if (job.state === 'done' && job.result) {
        const done = job as DoneJob
        const r = done.result
        if (r.rows.length === 0 && r.warnings.length === 0) {
          toast.show(`Rescan finished: no new projects, ${r.notFound} not found`)
          setPhase({ kind: 'leaving' })
          if (firstEntry) navigate('/', { replace: true })
          else navigate(-1)
        } else {
          setPhase({ kind: 'results', job: done })
        }
      } else {
        setPhase({
          kind: 'failed',
          error: job.error || NOT_FINISHED,
          workspaceId: retryScope(job),
        })
      }
    }

    const stop = watchScan(id, {
      progress: setProgress,
      end: () => {
        getScan(id, ctrl.signal)
          .then((job) => {
            if (!ctrl.signal.aborted) settle(job)
          })
          .catch((err) => {
            if (!ctrl.signal.aborted)
              setPhase({ kind: 'failed', error: message(err), workspaceId: 0 })
          })
      },
    })
    return () => {
      stop()
      ctrl.abort()
    }
  }, [id, navigate, toast, toOnboarding, firstEntry])

  const retry = async (workspaceId: number) => {
    setStarting(true)
    try {
      const { id: next } = await startRescan(workspaceId)
      navigate(`/rescan/${next}`, { replace: true, state: { first: firstEntry } })
    } catch (err) {
      if (err instanceof ApiError && err.status === 409 && isScanBusy(err.body)) {
        // A scan runs already: follow it.
        if (err.body.rescan) {
          navigate(`/rescan/${err.body.id}`, { replace: true, state: { first: firstEntry } })
        } else {
          navigate('/onboarding', { replace: true, state: { scanId: err.body.id } })
        }
        return
      }
      setPhase((ph) => (ph.kind === 'failed' ? { ...ph, error: message(err) } : ph))
      setStarting(false)
    }
  }

  const add = async (job: DoneJob, projects: TrackItem[]) => {
    setAdding(true)
    setTrackError('')
    try {
      const res = await trackScan(job.id, { projects })
      toast.show(`${plural(res.added, 'project', 'projects')} added`)
      navigate('/')
    } catch (err) {
      setTrackError(message(err))
      setAdding(false)
    }
  }

  let body: ReactNode = null
  if (phase.kind === 'scanning') {
    body = (
      <div className="flex w-full max-w-[640px] flex-col gap-4 rounded-[14px] border border-border bg-surface p-6">
        <ScanProgressView progress={progress} />
      </div>
    )
  } else if (phase.kind === 'failed') {
    body = (
      <div className="flex w-full max-w-[640px] flex-col gap-4 rounded-[14px] border border-border bg-surface p-6">
        <ScanFailed
          error={phase.error}
          busy={starting}
          label="Retry"
          onRetry={() => void retry(phase.workspaceId)}
        />
        <button
          type="button"
          onClick={() => navigate('/')}
          className="h-9 w-fit cursor-pointer rounded-lg border border-border-strong px-4 text-text"
        >
          Back to Dashboard
        </button>
      </div>
    )
  } else if (phase.kind === 'results') {
    const job = phase.job
    body = (
      <RescanResults
        key={job.id}
        job={job}
        busy={adding}
        error={trackError}
        onAdd={(projects) => void add(job, projects)}
        onBack={() => navigate('/')}
      />
    )
  }

  return (
    <div className="flex flex-col items-center gap-8">
      <header className="flex w-full max-w-[980px] items-center justify-between gap-4">
        <h1 className="text-2xl font-semibold tracking-tight">Rescan</h1>
      </header>
      {body}
    </div>
  )
}
