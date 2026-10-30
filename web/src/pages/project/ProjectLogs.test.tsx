import { act, fireEvent, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { LogLine, Project } from '../../api/types.gen'
import { FakeEventSource } from '../../test/fakeEventSource'
import { makeContainer, makeProject, pushState, renderProject } from '../../test/projects'

const LOGS = '/api/projects/1/logs'
const BASE = Date.UTC(2026, 9, 30, 10, 0, 0)

/** at is the RFC 3339 time of second n after BASE, with Docker's nanoseconds. */
const at = (n: number) => new Date(BASE + n * 1000).toISOString().replace('.000Z', '.000000001Z')

/** line is one log line of service svc at second n. */
const line = (n: number, text = `line ${n}`, svc = 'app'): LogLine => ({
  container: `shop-${svc}-1`,
  service: svc,
  time: at(n),
  text,
})

/** hms is an RFC 3339 time as the view shows it: local HH:MM:SS. */
const hms = (iso: string) => {
  const d = new Date(iso)
  return [d.getHours(), d.getMinutes(), d.getSeconds()]
    .map((v) => String(v).padStart(2, '0'))
    .join(':')
}

const running = makeProject({ containers: [makeContainer('app'), makeContainer('db')] })
const exited = (svc: string) =>
  makeContainer(svc, { state: 'exited', status: 'Exited', exitReason: 'Exited (0)' })

function source(url = LOGS): FakeEventSource {
  const es = FakeEventSource.latest(url)
  if (!es) throw new Error(`no source for ${url}`)
  return es
}

const send = (lines: LogLine[], url = LOGS) =>
  act(() => {
    source(url).emit('lines', { lines })
  })

const end = (reason: string, message = '', url = LOGS) =>
  act(() => {
    source(url).emit('end', { reason, message })
  })

/** open renders the Logs tab at path with the live state holding project. */
function open(path = '/projects/1/logs', project: Project = running, dockerUp = true) {
  const r = renderProject(path)
  pushState([project], dockerUp)
  return r
}

const logArea = () => screen.getByRole('log')
const rows = () => within(logArea()).queryAllByTestId('log-line')
const rowTexts = () => rows().map((r) => r.textContent ?? '')

/**
 * sizeLog gives the log area a layout jsdom lacks: a scroll height, a client
 * height and a settable scroll position. Change the returned box to resize it.
 */
function sizeLog(scrollHeight = 1000, clientHeight = 400) {
  const el = logArea()
  const box = { scrollHeight, clientHeight, scrollTop: 0 }
  Object.defineProperty(el, 'scrollHeight', { configurable: true, get: () => box.scrollHeight })
  Object.defineProperty(el, 'clientHeight', { configurable: true, get: () => box.clientHeight })
  Object.defineProperty(el, 'scrollTop', {
    configurable: true,
    get: () => box.scrollTop,
    set: (v: number) => {
      box.scrollTop = v
    },
  })
  return box
}

const status = () => screen.getByTestId('log-status')
const pauseButton = () => screen.getByRole('button', { name: /scrolling/ })

/** advance moves the fake clock and lets the timers' callbacks settle. */
const advance = (ms: number) =>
  act(async () => {
    await vi.advanceTimersByTimeAsync(ms)
  })

const scrollIntoView = vi.fn()

beforeEach(() => {
  FakeEventSource.instances = []
  vi.stubGlobal('EventSource', FakeEventSource)
  scrollIntoView.mockReset()
  // jsdom has no scrollIntoView.
  Element.prototype.scrollIntoView = scrollIntoView
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('project logs', () => {
  it('shows the backlog then live lines', () => {
    open()
    expect(FakeEventSource.forUrl(LOGS)).toHaveLength(1)
    expect(status()).toHaveTextContent('Live · last 500 lines')

    send([line(1, 'first'), line(2, 'second')])
    expect(rows()).toHaveLength(2)
    expect(rows()[0]).toHaveTextContent(hms(at(1)))
    expect(rows()[0]).toHaveTextContent('first')

    send([line(3, 'third')])
    expect(rows()).toHaveLength(3)
    expect(rows()[2]).toHaveTextContent('third')
  })

  it('holds at most 2000 lines', () => {
    open()
    let n = 0
    const batch = () => Array.from({ length: 500 }, () => line(n, `line ${n++}`))
    send(batch())
    for (let i = 0; i < 4; i++) send(batch())
    expect(rows()).toHaveLength(2000)
    expect(rows()[0]).toHaveTextContent('line 500')
    expect(rows()[1999]).toHaveTextContent('line 2499')
  })

  it('pause stops autoscroll and counts new lines', () => {
    open()
    const box = sizeLog()
    send([line(1), line(2)])
    expect(box.scrollTop).toBe(1000)

    fireEvent.click(screen.getByRole('button', { name: 'Pause scrolling' }))
    expect(pauseButton()).toHaveAccessibleName('Resume scrolling')
    expect(pauseButton()).toHaveAttribute('aria-pressed', 'true')
    expect(status()).toHaveTextContent('Paused')

    box.scrollHeight = 1200
    send([line(3), line(4), line(5)])
    expect(rows()).toHaveLength(5)
    expect(box.scrollTop).toBe(1000)
    expect(status()).toHaveTextContent('Paused · 3 new lines')

    send([line(6)])
    expect(status()).toHaveTextContent('Paused · 4 new lines')
  })

  it('scrolling up pauses', () => {
    open()
    const box = sizeLog()
    send([line(1), line(2)])
    expect(pauseButton()).toHaveAccessibleName('Pause scrolling')

    box.scrollTop = 100
    fireEvent.scroll(logArea())
    expect(pauseButton()).toHaveAccessibleName('Resume scrolling')
    expect(status()).toHaveTextContent('Paused')
  })

  it('resume jumps to the newest line', () => {
    open()
    const box = sizeLog()
    send([line(1)])
    fireEvent.click(screen.getByRole('button', { name: 'Pause scrolling' }))
    box.scrollTop = 100
    box.scrollHeight = 1500
    send([line(2)])
    expect(box.scrollTop).toBe(100)

    fireEvent.click(screen.getByRole('button', { name: 'Resume scrolling' }))
    expect(box.scrollTop).toBe(1500)
    expect(status()).toHaveTextContent('Live · last 500 lines')
    expect(pauseButton()).toHaveAttribute('aria-pressed', 'false')
  })

  it('search filters and highlights', () => {
    open()
    send([line(1, 'GET /health 200'), line(2, 'payment Failed'), line(3, 'retry failed again')])
    const search = screen.getByRole('searchbox', { name: 'Search logs' })

    fireEvent.change(search, { target: { value: 'FAIL' } })
    expect(rowTexts()).toHaveLength(2)
    const marks = Array.from(logArea().querySelectorAll('mark')).map((m) => m.textContent)
    expect(marks).toEqual(['Fail', 'fail'])

    fireEvent.change(search, { target: { value: 'nothing like this' } })
    expect(rows()).toHaveLength(0)
    expect(logArea()).toHaveTextContent('No line matches the search.')

    fireEvent.change(search, { target: { value: '' } })
    expect(rows()).toHaveLength(3)
    expect(logArea().querySelectorAll('mark')).toHaveLength(0)
  })

  it('download saves the held lines with the project name and time', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date(2026, 9, 30, 14, 5, 9))
    const blobs: Blob[] = []
    const createObjectURL = vi.fn((b: Blob) => {
      blobs.push(b)
      return 'blob:logs'
    })
    const revokeObjectURL = vi.fn()
    vi.stubGlobal('URL', Object.assign(URL, { createObjectURL, revokeObjectURL }))
    const saved: Array<{ name: string; href: string }> = []
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (
      this: HTMLAnchorElement,
    ) {
      saved.push({ name: this.download, href: this.href })
    })

    open('/projects/1/logs', makeProject({ ...running, displayName: 'My Shop API' }))
    send([line(1, 'first'), line(2, 'second', 'db')])
    // Search changes what is shown, not what is saved.
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search logs' }), {
      target: { value: 'first' },
    })
    fireEvent.click(screen.getByRole('button', { name: 'Download' }))

    expect(saved).toEqual([{ name: 'my-shop-api-logs-20261030-140509.txt', href: 'blob:logs' }])
    expect(await blobs[0].text()).toBe(`${at(1)} shop-app-1 first\n${at(2)} shop-db-1 second\n`)
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:logs')
  })

  it('level words and colours', () => {
    open()
    send([
      line(1, 'ERROR boom'),
      line(2, 'WARN slow'),
      line(3, 'INFO ok'),
      line(4, 'DEBUG detail'),
      line(5, 'FATAL dead'),
      line(6, 'plain line'),
    ])
    const levels = rows().map((r) => within(r).getByTestId('log-level'))
    expect(levels.map((l) => l.textContent)).toEqual([
      'ERROR',
      'WARN ',
      'INFO ',
      'DEBUG',
      'FATAL',
      '',
    ])
    expect(levels[0]).toHaveStyle({ color: '#FF8C8F' })
    expect(levels[1]).toHaveStyle({ color: '#F2CF6B' })
    expect(levels[2]).toHaveStyle({ color: '#7FB8FF' })
    expect(levels[3]).toHaveStyle({ color: '#949CA8' })
    expect(levels[4]).toHaveStyle({ color: '#FF8C8F' })
  })

  it('focus=error preselects the container, scrolls to the newest error and starts paused', () => {
    open('/projects/1/logs?container=app&focus=error')
    expect(FakeEventSource.forUrl(LOGS)).toHaveLength(0)
    expect(screen.getByRole('combobox', { name: 'Container' })).toHaveValue('app')

    send(
      [
        line(1, 'INFO a'),
        line(2, 'ERROR first error'),
        line(3, 'INFO b'),
        line(4, 'FATAL newest error'),
        line(5, 'INFO c'),
      ],
      `${LOGS}?service=app`,
    )
    const focused = rows().filter((r) => r.getAttribute('aria-current') === 'true')
    expect(focused).toHaveLength(1)
    expect(focused[0]).toHaveTextContent('newest error')
    expect(scrollIntoView).toHaveBeenCalled()
    expect(scrollIntoView.mock.contexts.at(-1)).toBe(focused[0])
    expect(pauseButton()).toHaveAccessibleName('Resume scrolling')
    expect(screen.queryByText('No error in the last 500 lines')).not.toBeInTheDocument()

    send([line(6, 'INFO later')], `${LOGS}?service=app`)
    expect(status()).toHaveTextContent('Paused · 1 new line')
  })

  it('no error shows the note', () => {
    open('/projects/1/logs?container=app&focus=error')
    send([line(1, 'INFO a'), line(2, 'INFO b')], `${LOGS}?service=app`)
    expect(screen.getByText('No error in the last 500 lines')).toBeInTheDocument()
    expect(rows().filter((r) => r.getAttribute('aria-current') === 'true')).toHaveLength(0)
    expect(pauseButton()).toHaveAccessibleName('Pause scrolling')
  })

  it('reopens when a container of the selection is running or restarting again', () => {
    open('/projects/1/logs?container=app')
    const url = `${LOGS}?service=app`
    send([line(1, 'boot')], url)
    send([line(2, 'serving')], url)

    // The app stops: the follow exits after live lines.
    pushState([makeProject({ containers: [exited('app'), makeContainer('db')] })])
    end('exited', '', url)
    expect(source(url).closed).toBe(true)
    expect(status()).toHaveTextContent('Stream ended: the containers stopped')

    // Another service running is not the selection: nothing reopens.
    pushState([makeProject({ containers: [exited('app'), makeContainer('db')] })])
    expect(FakeEventSource.forUrl(url)).toHaveLength(1)

    // The app restarts (a crash loop counts): the stream reopens and keeps the lines.
    pushState([
      makeProject({
        status: 'error',
        containers: [makeContainer('app', { state: 'restarting' }), makeContainer('db')],
      }),
    ])
    expect(FakeEventSource.forUrl(url)).toHaveLength(2)
    send([line(1, 'boot'), line(2, 'serving'), line(3, 'booting again')], url)
    expect(rows()).toHaveLength(3)
    expect(rows()[0]).toHaveTextContent('boot')
    expect(rows()[1]).toHaveTextContent('serving')
    expect(rows()[2]).toHaveTextContent('booting again')
    expect(status()).toHaveTextContent('Live · last 500 lines')
  })

  it('a stopped project is Not live, then reopens when it runs', () => {
    open('/projects/1/logs', makeProject({ status: 'stopped', containers: [exited('app')] }))
    send([line(1, 'old line')])
    end('exited')
    expect(status()).toHaveTextContent('Not live: no container is running')
    expect(rows()).toHaveLength(1)

    pushState([makeProject({ containers: [makeContainer('app')] })])
    expect(FakeEventSource.forUrl(LOGS)).toHaveLength(2)
  })

  it('an exited end while the live state still shows a running container retries after the backoff', async () => {
    vi.useFakeTimers()
    open()
    send([line(1)])
    end('exited')
    expect(FakeEventSource.forUrl(LOGS)).toHaveLength(1)
    await advance(999)
    expect(FakeEventSource.forUrl(LOGS)).toHaveLength(1)
    await advance(1)
    expect(FakeEventSource.forUrl(LOGS)).toHaveLength(2)
  })

  it('too many shows retry and does not retry by itself', async () => {
    vi.useFakeTimers()
    open()
    end('too_many_log_streams')
    expect(source().closed).toBe(true)
    expect(
      screen.getByText('Too many log views are open on the panel. Close one and retry.'),
    ).toBeInTheDocument()

    await advance(60_000)
    expect(FakeEventSource.forUrl(LOGS)).toHaveLength(1)

    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(FakeEventSource.forUrl(LOGS)).toHaveLength(2)
    send([line(1)])
    expect(
      screen.queryByText('Too many log views are open on the panel. Close one and retry.'),
    ).not.toBeInTheDocument()
  })

  it('docker down shows its message and reopens when Docker is up', () => {
    open('/projects/1/logs', makeProject({ status: 'unknown', containers: [] }), false)
    end('docker_down')
    expect(screen.getByText("Docker is not responding, so logs can't be read.")).toBeInTheDocument()

    pushState([makeProject({ status: 'unknown', containers: [] })], false)
    expect(FakeEventSource.forUrl(LOGS)).toHaveLength(1)

    pushState([running], true)
    expect(FakeEventSource.forUrl(LOGS)).toHaveLength(2)
  })

  it('no containers in the selection shows No logs, containers but none running shows Not live', () => {
    const notCreated = makeContainer('cache', { name: 'cache', state: '', status: 'Not created' })
    const first = open(
      '/projects/1/logs',
      makeProject({ status: 'stopped', containers: [notCreated] }),
    )
    send([])
    end('exited')
    expect(screen.getByText('No logs: the project has no containers.')).toBeInTheDocument()
    expect(screen.queryByText('Not live: no container is running')).not.toBeInTheDocument()
    first.unmount()

    FakeEventSource.instances = []
    open('/projects/1/logs', makeProject({ status: 'stopped', containers: [exited('app')] }))
    send([])
    end('exited')
    expect(screen.getByText('Not live: no container is running')).toBeInTheDocument()
    expect(screen.queryByText('No logs: the project has no containers.')).not.toBeInTheDocument()
  })

  it('slow or a network error reopens with the backoff and keeps the lines held', async () => {
    vi.useFakeTimers()
    open()
    send([line(1), line(2)])
    end('slow')
    await advance(1000)
    expect(FakeEventSource.forUrl(LOGS)).toHaveLength(2)
    // The new backlog overlaps what is held: nothing is shown twice.
    send([line(1), line(2), line(3)])
    expect(rows()).toHaveLength(3)

    act(() => source().fail())
    expect(source().closed).toBe(true)
    await advance(1000)
    expect(FakeEventSource.forUrl(LOGS)).toHaveLength(3)
    send([line(2), line(3), line(4)])
    expect(rows()).toHaveLength(4)
    expect(status()).toHaveTextContent('Live · last 500 lines')
  })

  it('network errors in a row back off 1 s, then 2 s', async () => {
    vi.useFakeTimers()
    open()
    act(() => source().fail())
    await advance(1000)
    expect(FakeEventSource.forUrl(LOGS)).toHaveLength(2)
    act(() => source().closeWithError())
    await advance(1999)
    expect(FakeEventSource.forUrl(LOGS)).toHaveLength(2)
    await advance(1)
    expect(FakeEventSource.forUrl(LOGS)).toHaveLength(3)
  })

  it('an unknown service falls back to All and reopens', () => {
    const { router } = open('/projects/1/logs?container=gone')
    expect(screen.getByRole('combobox', { name: 'Container' })).toHaveValue('gone')
    end('unknown_service', '', `${LOGS}?service=gone`)
    expect(FakeEventSource.forUrl(LOGS)).toHaveLength(1)
    expect(screen.getByRole('combobox', { name: 'Container' })).toHaveValue('')
    expect(router.state.location.search).toBe('')
  })

  it('a missing folder says so', () => {
    open('/projects/1/logs', makeProject({ status: 'notfound', containers: [] }))
    end('not_found')
    expect(
      screen.getByText("The project folder is missing, so logs can't be read."),
    ).toBeInTheDocument()
  })

  it('an error end shows its message and a Retry', () => {
    open()
    end('error', 'could not read the logs')
    expect(screen.getByText(/could not read the logs/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(FakeEventSource.forUrl(LOGS)).toHaveLength(2)
  })

  it('changing the container reopens the stream and clears the lines', () => {
    open()
    send([line(1), line(2, 'db line', 'db')])
    const select = screen.getByRole('combobox', { name: 'Container' })
    expect(Array.from((select as HTMLSelectElement).options).map((o) => o.text)).toEqual([
      'All',
      'app',
      'db',
    ])

    fireEvent.change(select, { target: { value: 'db' } })
    expect(source().closed).toBe(true)
    expect(FakeEventSource.forUrl(`${LOGS}?service=db`)).toHaveLength(1)
    expect(rows()).toHaveLength(0)
  })

  it('closes the stream when the tab unmounts', async () => {
    const { router } = open()
    await act(() => router.navigate('/projects/1'))
    expect(source().closed).toBe(true)
  })

  it('toolbar wraps at 375 px', () => {
    open()
    expect(screen.getByTestId('log-toolbar')).toHaveClass('flex-wrap')
  })

  it('with All shows the service name', () => {
    open()
    send([line(1, 'hello', 'app'), line(2, 'query', 'db')])
    expect(rows().map((r) => within(r).getByTestId('log-service').textContent)).toEqual([
      'app',
      'db',
    ])
  })

  it('with one container shows no service name', () => {
    open('/projects/1/logs?container=app')
    send([line(1, 'hello')], `${LOGS}?service=app`)
    expect(within(rows()[0]).queryByTestId('log-service')).not.toBeInTheDocument()
  })
})
