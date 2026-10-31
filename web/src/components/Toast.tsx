import { X } from 'lucide-react'
import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import { Link } from 'react-router'

const TOAST_MS = 4000
const MAX_TOASTS = 4

type ToastItem = {
  id: number
  kind: 'success' | 'error'
  title: ReactNode
  detail?: string
  link?: ToastLink
}

/** ToastLink is an in-app link under an error toast's detail ("View disk usage by app"). */
export type ToastLink = { label: string; to: string }

type ToastApi = {
  show(message: ReactNode): void
  success(message: ReactNode): void
  error(title: ReactNode, detail?: string, link?: ToastLink): void
}

const ToastContext = createContext<ToastApi | null>(null)

/** push appends a toast. At the cap, the oldest existing success goes first, or the oldest error when all are errors; the new toast always shows. */
function push(list: ToastItem[], next: ToastItem): ToastItem[] {
  const out = [...list]
  if (out.length >= MAX_TOASTS) {
    const victim = out.findIndex((t) => t.kind === 'success')
    out.splice(victim === -1 ? 0 : victim, 1)
  }
  out.push(next)
  return out
}

/**
 * ToastProvider shows a stack of toasts, bottom right, newest last, at most four.
 * A success toast clears itself after 4 seconds; an error stays until it is closed.
 */
export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<ToastItem[]>([])
  const nextId = useRef(0)
  const timers = useRef(new Set<ReturnType<typeof setTimeout>>())

  const dismiss = useCallback((id: number) => {
    setToasts((list) => list.filter((t) => t.id !== id))
  }, [])

  const success = useCallback(
    (message: ReactNode) => {
      const id = nextId.current++
      setToasts((list) => push(list, { id, kind: 'success', title: message }))
      const timer = setTimeout(() => {
        timers.current.delete(timer)
        dismiss(id)
      }, TOAST_MS)
      timers.current.add(timer)
    },
    [dismiss],
  )

  const error = useCallback((title: ReactNode, detail?: string, link?: ToastLink) => {
    const id = nextId.current++
    setToasts((list) => push(list, { id, kind: 'error', title, detail, link }))
  }, [])

  useEffect(() => {
    const pending = timers.current
    return () => {
      for (const timer of pending) clearTimeout(timer)
      pending.clear()
    }
  }, [])

  const api = useMemo(() => ({ show: success, success, error }), [success, error])

  return (
    <ToastContext.Provider value={api}>
      {children}
      <div
        aria-live="polite"
        data-testid="toast-region"
        className="pointer-events-none fixed right-6 bottom-6 z-50 flex flex-col items-end gap-2.5"
      >
        {toasts.map((t) =>
          t.kind === 'success' ? (
            <div
              key={t.id}
              data-testid="toast-success"
              className="pointer-events-auto flex items-center gap-2.5 rounded-[10px] border border-success-bg bg-surface-2 px-4 py-3 text-text shadow-[0_12px_32px_rgba(0,0,0,0.5)]"
            >
              <span aria-hidden="true" className="size-2 flex-none rounded-full bg-success" />
              <span>{t.title}</span>
            </div>
          ) : (
            <div
              key={t.id}
              role="alert"
              className="pointer-events-auto flex max-w-[420px] items-start gap-2.5 rounded-[10px] border border-error-border bg-error-soft px-4 py-3 text-text shadow-[0_12px_32px_rgba(0,0,0,0.5)]"
            >
              <span aria-hidden="true" className="mt-1.5 size-2 flex-none rounded-full bg-danger" />
              <div className="flex min-w-0 flex-col gap-1">
                <span className="font-semibold">{t.title}</span>
                {t.detail ? (
                  <span className="whitespace-pre-line text-[13px] text-error-text">
                    {t.detail}
                  </span>
                ) : null}
                {t.link ? (
                  <Link
                    to={t.link.to}
                    onClick={() => dismiss(t.id)}
                    className="text-[13px] text-error-fg hover:text-white"
                  >
                    {t.link.label}
                  </Link>
                ) : null}
              </div>
              <button
                type="button"
                aria-label="Dismiss"
                onClick={() => dismiss(t.id)}
                className="-my-1 -mr-2 ml-1 flex size-6 flex-none cursor-pointer items-center justify-center rounded-md border-0 bg-transparent text-error-text focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-accent"
              >
                <X aria-hidden="true" size={14} strokeWidth={2} />
              </button>
            </div>
          ),
        )}
      </div>
    </ToastContext.Provider>
  )
}

export function useToast(): ToastApi {
  const ctx = useContext(ToastContext)
  if (!ctx) throw new Error('useToast must be used inside a ToastProvider')
  return ctx
}
