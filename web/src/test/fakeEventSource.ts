/** A minimal EventSource for jsdom, which has none. Tests install it with vi.stubGlobal. */
export class FakeEventSource {
  static instances: FakeEventSource[] = []
  url: string
  closed = false
  listeners = new Map<string, Set<(e: MessageEvent) => void>>()

  constructor(url: string) {
    this.url = url
    FakeEventSource.instances.push(this)
  }

  addEventListener(type: string, fn: (e: MessageEvent) => void) {
    if (!this.listeners.has(type)) this.listeners.set(type, new Set())
    this.listeners.get(type)?.add(fn)
  }

  removeEventListener(type: string, fn: (e: MessageEvent) => void) {
    this.listeners.get(type)?.delete(fn)
  }

  close() {
    this.closed = true
  }

  emit(type: string, data: unknown) {
    for (const fn of this.listeners.get(type) ?? []) {
      fn(new MessageEvent(type, { data: typeof data === 'string' ? data : JSON.stringify(data) }))
    }
  }
}
