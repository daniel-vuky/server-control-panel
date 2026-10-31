import { Download, Pause, Play, Search } from 'lucide-react'
import { memo, type ReactNode, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { useSearchParams } from 'react-router'
import { LEVEL_COLOR } from '../../lib/logLevel'
import { plural } from '../../lib/plural'
import {
  type HeldLine,
  type LogStreamState,
  selectionContainers,
  useLogStream,
} from '../../lib/useLogStream'
import { useProjectOutlet } from './projectContext'

/** Scrolled further than this from the bottom, the view pauses. */
const PAUSE_DISTANCE_PX = 24

const BUTTON =
  'flex h-[34px] cursor-pointer items-center gap-[7px] rounded-[7px] border border-border-strong bg-surface-2 px-3 text-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:text-disabled-text'

/**
 * ProjectLogs is the Logs tab: a live view of the project's container output,
 * for all containers or one service (?container=<service>). ?focus=error
 * opens on the newest ERROR or FATAL line of the backlog, paused.
 */
export default function ProjectLogs() {
  const { project } = useProjectOutlet()
  const [params, setParams] = useSearchParams()
  const service = params.get('container') ?? ''
  const focusPending = useRef(params.get('focus') === 'error')

  const [search, setSearch] = useState('')
  const [paused, setPaused] = useState(false)
  // pausedAt is newCount when the view paused; the difference is the new lines.
  const [pausedAt, setPausedAt] = useState(0)
  const [focusId, setFocusId] = useState<number | null>(null)
  const [noError, setNoError] = useState(false)
  const logRef = useRef<HTMLDivElement>(null)

  /** select shows another container (or All), dropping the focus of the link that opened the tab. */
  const select = (next: string) => {
    focusPending.current = false
    setFocusId(null)
    setNoError(false)
    setPausedAt(0)
    setParams(next === '' ? {} : { container: next }, { replace: true })
  }

  const stream = useLogStream(project, service, {
    onBacklog: (held) => {
      if (!focusPending.current) return
      focusPending.current = false
      const error = held.findLast((l) => l.level === 'ERROR' || l.level === 'FATAL')
      if (!error) {
        setNoError(true)
        return
      }
      setFocusId(error.id)
      setPaused(true)
      setPausedAt(0)
    },
    onUnknownService: () => select(''),
  })

  const matcher = useMemo(
    () => (search === '' ? null : new RegExp(escapeRegExp(search), 'i')),
    [search],
  )
  const shown = useMemo(
    () => (matcher ? stream.lines.filter((l) => matcher.test(l.text)) : stream.lines),
    [stream.lines, matcher],
  )

  // Auto-scroll keeps the newest line in view unless paused.
  // biome-ignore lint/correctness/useExhaustiveDependencies: every change to the shown lines scrolls.
  useLayoutEffect(() => {
    if (paused) return
    const el = logRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [paused, shown])

  // focus=error: bring the newest error into view once.
  useLayoutEffect(() => {
    if (focusId === null) return
    logRef.current?.querySelector('[aria-current="true"]')?.scrollIntoView({ block: 'center' })
  }, [focusId])

  const pause = () => {
    setPaused(true)
    setPausedAt(stream.newCount)
  }

  const onScroll = () => {
    const el = logRef.current
    if (!el || paused) return
    if (el.scrollHeight - el.scrollTop - el.clientHeight > PAUSE_DISTANCE_PX) pause()
  }

  const services = useMemo(() => {
    // One option per service, labelled with its first container's name (design 02).
    const list: { service: string; label: string }[] = []
    for (const c of project.containers) {
      if (c.service !== '' && !list.some((o) => o.service === c.service)) {
        list.push({ service: c.service, label: c.name || c.service })
      }
    }
    // A preselected service the project does not list stays shown until the server says so.
    if (service !== '' && !list.some((o) => o.service === service)) {
      list.push({ service, label: service })
    }
    return list
  }, [project.containers, service])

  const hasContainers = selectionContainers(project, service).length > 0
  const showService = service === ''

  return (
    <section aria-label="Logs" className="flex flex-col gap-3">
      <div data-testid="log-toolbar" className="flex flex-wrap items-center gap-2.5">
        <label className="flex h-[34px] items-center gap-2 rounded-[7px] border border-border-strong bg-surface px-2.5 text-[13px] text-muted focus-within:outline-2 focus-within:outline-accent">
          Container
          <select
            aria-label="Container"
            value={service}
            onChange={(e) => select(e.target.value)}
            className="max-w-[40vw] border-0 bg-transparent font-mono text-[13px] text-text outline-none"
          >
            <option value="">All</option>
            {services.map((o) => (
              <option key={o.service} value={o.service}>
                {o.label}
              </option>
            ))}
          </select>
        </label>
        <label className="flex h-[34px] max-w-[360px] flex-[1_1_220px] items-center gap-2 rounded-[7px] border border-border-strong bg-surface px-2.5 focus-within:outline-2 focus-within:outline-accent">
          <Search aria-hidden="true" size={15} strokeWidth={2} className="flex-none text-muted" />
          <span className="sr-only">Search logs</span>
          <input
            type="search"
            placeholder="Search logs…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="min-w-0 flex-1 border-0 bg-transparent text-[13px] text-text outline-none"
          />
        </label>
        <StreamStatus
          state={stream.state}
          paused={paused}
          newLines={stream.newCount - pausedAt}
          hasContainers={hasContainers}
        />
        <div className="ml-auto flex gap-2">
          <button
            type="button"
            aria-pressed={paused}
            onClick={() => (paused ? setPaused(false) : pause())}
            className={BUTTON}
          >
            {paused ? (
              <Play aria-hidden="true" size={14} fill="currentColor" strokeWidth={0} />
            ) : (
              <Pause aria-hidden="true" size={14} fill="currentColor" strokeWidth={0} />
            )}
            {paused ? 'Resume scrolling' : 'Pause scrolling'}
          </button>
          <button
            type="button"
            disabled={stream.lines.length === 0}
            onClick={() => downloadLines(project.displayName, stream.lines)}
            className={BUTTON}
          >
            <Download aria-hidden="true" size={15} strokeWidth={1.9} />
            Download
          </button>
        </div>
      </div>

      <StreamNotice state={stream.state} message={stream.message} retry={stream.retry} />
      {noError && <p className="m-0 text-[13px] text-muted">No error in the last 500 lines</p>}

      <div
        ref={logRef}
        role="log"
        aria-label={service === '' ? 'Log of all containers' : `Log of ${service}`}
        onScroll={onScroll}
        className="h-[560px] max-h-[70vh] overflow-auto rounded-[10px] border border-border bg-[#07090b] py-3.5 font-mono text-[12.5px] leading-[1.75]"
      >
        {shown.map((l) => (
          <Line
            key={l.id}
            line={l}
            showService={showService}
            matcher={matcher}
            focused={l.id === focusId}
          />
        ))}
        {matcher && shown.length === 0 && stream.lines.length > 0 && (
          <p className="m-0 px-4 font-sans text-[13px] text-muted">No line matches the search.</p>
        )}
      </div>
    </section>
  )
}

type StatusProps = {
  state: LogStreamState
  paused: boolean
  newLines: number
  hasContainers: boolean
}

/** StreamStatus is the toolbar's label: live, paused with its new lines, or why it is not live. */
function StreamStatus({ state, paused, newLines, hasContainers }: StatusProps) {
  let tone: 'live' | 'muted' | 'ended'
  let text: string
  if (state === 'live') {
    tone = paused ? 'muted' : 'live'
    text = paused
      ? `Paused${newLines > 0 ? ` · ${plural(newLines, 'new line', 'new lines')}` : ''}`
      : 'Live · last 500 lines'
  } else if (state === 'notLive') {
    tone = 'muted'
    text = hasContainers
      ? 'Not live: no container is running'
      : 'No logs: the project has no containers.'
  } else if (state === 'ended') {
    tone = 'ended'
    text = 'Stream ended: the containers stopped'
  } else {
    tone = 'ended'
    text = 'Stream ended'
  }
  const colour = { live: 'text-success-text', muted: 'text-muted', ended: 'text-danger-text' }[tone]
  const dot = {
    live: 'animate-pulse bg-success',
    muted: 'bg-disabled-text',
    ended: 'bg-danger',
  }[tone]
  return (
    <span
      data-testid="log-status"
      className={`flex items-center gap-[7px] text-[12.5px] ${colour}`}
    >
      <span aria-hidden="true" className={`size-[7px] flex-none rounded-full ${dot}`} />
      {text}
    </span>
  )
}

/** StreamNotice says why the stream is not open, with Retry where only a retry reopens it. */
function StreamNotice({
  state,
  message,
  retry,
}: {
  state: LogStreamState
  message: string
  retry: () => void
}) {
  let text: string
  let canRetry = false
  switch (state) {
    case 'tooMany':
      text = 'Too many log views are open on the panel. Close one and retry.'
      canRetry = true
      break
    case 'dockerDown':
      text = "Docker is not responding, so logs can't be read."
      break
    case 'missing':
      text = "The project folder is missing, so logs can't be read."
      break
    case 'error':
      text = message === '' ? "Logs can't be read." : `Logs can't be read: ${message}`
      canRetry = true
      break
    default:
      return null
  }
  return (
    <div
      role="alert"
      className="flex flex-wrap items-center gap-3 rounded-[10px] border border-error-border bg-surface px-4 py-3 text-[13px] text-error-text"
    >
      <span className="min-w-0 break-words">{text}</span>
      {canRetry && (
        <button type="button" onClick={retry} className={`${BUTTON} ml-auto h-8`}>
          Retry
        </button>
      )}
    </div>
  )
}

type LineProps = {
  line: HeldLine
  showService: boolean
  matcher: RegExp | null
  focused: boolean
}

/** Line is one log line of design 02: local time, level word, service (with All), message. */
const Line = memo(function Line({ line, showService, matcher, focused }: LineProps) {
  return (
    <div
      data-testid="log-line"
      aria-current={focused ? 'true' : undefined}
      className={`flex gap-[14px] px-4 whitespace-pre ${
        focused ? 'bg-error-bg shadow-[inset_2px_0_0_var(--color-danger-text)]' : ''
      }`}
    >
      <span className="flex-none text-disabled-text">{clock(line.time)}</span>
      <span
        data-testid="log-level"
        className="w-[5ch] flex-none"
        style={line.level ? { color: LEVEL_COLOR[line.level], fontWeight: 600 } : undefined}
      >
        {line.level ? line.level.padEnd(5, ' ') : ''}
      </span>
      {showService && (
        <span data-testid="log-service" className="flex-none text-muted">
          {line.service || line.container}
        </span>
      )}
      <span className="text-text-2">{highlight(line.text, matcher)}</span>
    </div>
  )
})

const two = (n: number) => String(n).padStart(2, '0')

/** clock is an RFC 3339 time as local HH:MM:SS. */
function clock(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '--:--:--'
  return `${two(d.getHours())}:${two(d.getMinutes())}:${two(d.getSeconds())}`
}

function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

/** highlight wraps each match of matcher in text in a mark. */
function highlight(text: string, matcher: RegExp | null): ReactNode {
  if (!matcher) return text
  // Splitting on a capture group keeps the matches at the odd indexes.
  const parts = text.split(new RegExp(`(${matcher.source})`, 'gi'))
  const out: ReactNode[] = []
  for (let i = 0; i < parts.length; i++) {
    if (parts[i] === '') continue
    out.push(
      i % 2 === 1 ? (
        <mark key={i} className="rounded-sm bg-[#5c4a12] text-white">
          {parts[i]}
        </mark>
      ) : (
        parts[i]
      ),
    )
  }
  return out
}

/** slug is a display name as a file name part: lower case, dashes, never empty. */
function slug(name: string): string {
  const s = name
    .normalize('NFKD')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
  return s === '' ? 'project' : s
}

/** logFileName is <slug>-logs-YYYYMMDD-HHMMSS.txt in local time. */
function logFileName(name: string, now: Date): string {
  const date = `${now.getFullYear()}${two(now.getMonth() + 1)}${two(now.getDate())}`
  const time = `${two(now.getHours())}${two(now.getMinutes())}${two(now.getSeconds())}`
  return `${slug(name)}-logs-${date}-${time}.txt`
}

/** downloadLines saves the held lines raw: "<RFC 3339 time> <container> <text>" each. */
function downloadLines(name: string, lines: HeldLine[]) {
  const body = lines.map((l) => `${l.time} ${l.container} ${l.text}\n`).join('')
  const url = URL.createObjectURL(new Blob([body], { type: 'text/plain;charset=utf-8' }))
  const a = document.createElement('a')
  a.href = url
  a.download = logFileName(name, new Date())
  document.body.append(a)
  a.click()
  a.remove()
  // Revoke once the click has been handled; some browsers read the URL after it returns.
  setTimeout(() => URL.revokeObjectURL(url), 0)
}
