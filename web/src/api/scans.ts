import { getJSON, postJSON } from './client'
import type {
  ScanJob,
  ScanProgress,
  ScanRequest,
  ScanStarted,
  TrackRequest,
  TrackResponse,
} from './types.gen'

/** A 422 from startScan has a FieldError body; a 409 has a ScanBusy body. */
export function startScan(req: ScanRequest): Promise<ScanStarted> {
  return postJSON<ScanStarted>('/api/scans', req)
}

export function getScan(id: number, signal?: AbortSignal): Promise<ScanJob> {
  return getJSON<ScanJob>(`/api/scans/${id}`, signal)
}

export function trackScan(id: number, req: TrackRequest): Promise<TrackResponse> {
  return postJSON<TrackResponse>(`/api/scans/${id}/track`, req)
}

function parseData<T>(e: Event): T | null {
  try {
    return JSON.parse((e as MessageEvent<string>).data) as T
  } catch {
    return null
  }
}

/**
 * watchScan follows a scan over SSE. `end` fires once, with done, failed or
 * interrupted. If the connection drops before an end event, the job is read
 * once: if it is no longer running the watch ends with its state; if it still
 * runs, the EventSource is left to reconnect. The returned function stops the
 * watch.
 */
export function watchScan(
  id: number,
  on: { progress(p: ScanProgress): void; end(state: string): void },
): () => void {
  const source = new EventSource(`/api/scans/${id}/events`)
  let finished = false

  const finish = (state: string) => {
    if (finished) return
    finished = true
    source.close()
    on.end(state)
  }

  source.addEventListener('progress', (e) => {
    if (finished) return
    const p = parseData<ScanProgress>(e)
    if (p) on.progress(p)
  })

  source.addEventListener('end', (e) => {
    const data = parseData<{ state?: unknown }>(e)
    finish(typeof data?.state === 'string' ? data.state : 'failed')
  })

  source.addEventListener('error', () => {
    if (finished) return
    getScan(id)
      .then((job) => {
        if (job.state !== 'running') finish(job.state)
      })
      .catch(() => {
        // The job cannot be read right now; EventSource keeps retrying.
      })
  })

  return () => {
    finished = true
    source.close()
  }
}
