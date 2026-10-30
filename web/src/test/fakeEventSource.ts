type Listener = (e: MessageEvent) => void

/**
 * A minimal EventSource for jsdom, which has none. Tests install it with vi.stubGlobal.
 * Every instance is kept in `instances`; `forUrl` and `latest` pick them by URL.
 */
export class FakeEventSource {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSED = 2
  static instances: FakeEventSource[] = []

  /** forUrl lists the instances created for url, oldest first. */
  static forUrl(url: string): FakeEventSource[] {
    return FakeEventSource.instances.filter((es) => es.url === url)
  }

  /** latest is the newest instance created for url. */
  static latest(url: string): FakeEventSource | undefined {
    const list = FakeEventSource.forUrl(url)
    return list[list.length - 1]
  }

  readonly CONNECTING = 0
  readonly OPEN = 1
  readonly CLOSED = 2
  url: string
  readyState = 0
  closed = false
  onopen: ((e: MessageEvent) => void) | null = null
  onerror: ((e: MessageEvent) => void) | null = null
  listeners = new Map<string, Set<Listener>>()

  constructor(url: string) {
    this.url = url
    FakeEventSource.instances.push(this)
  }

  addEventListener(type: string, fn: Listener) {
    if (!this.listeners.has(type)) this.listeners.set(type, new Set())
    this.listeners.get(type)?.add(fn)
  }

  removeEventListener(type: string, fn: Listener) {
    this.listeners.get(type)?.delete(fn)
  }

  /** close is the EventSource API: the page closes the source; no event fires. */
  close() {
    this.closed = true
    this.readyState = FakeEventSource.CLOSED
  }

  /** emit delivers one message event; a message means the source is open. */
  emit(type: string, data: unknown) {
    if (this.readyState === FakeEventSource.CONNECTING) this.readyState = FakeEventSource.OPEN
    this.dispatch(type, typeof data === 'string' ? data : JSON.stringify(data))
  }

  /** open: the connection is established. */
  open() {
    this.readyState = FakeEventSource.OPEN
    this.dispatch('open')
  }

  /** fail: a network error; the browser keeps retrying, so the source stays CONNECTING. */
  fail() {
    this.readyState = FakeEventSource.CONNECTING
    this.dispatch('error')
  }

  /**
   * closeWithError: the server answered with something the browser will not retry
   * (a non-200, as an expired Access session gives), so the source becomes CLOSED.
   * Named apart from the API's close(), which the page itself calls.
   */
  closeWithError() {
    this.readyState = FakeEventSource.CLOSED
    this.dispatch('error')
  }

  private dispatch(type: string, data?: string) {
    const e = new MessageEvent(type, data === undefined ? {} : { data })
    if (type === 'open') this.onopen?.(e)
    if (type === 'error') this.onerror?.(e)
    for (const fn of this.listeners.get(type) ?? []) fn(e)
  }
}
