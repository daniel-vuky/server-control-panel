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

const TOAST_MS = 4000

type ToastApi = { show(message: string): void }

const ToastContext = createContext<ToastApi | null>(null)

/** ToastProvider shows one toast at a time, bottom right, for 4 seconds. */
export function ToastProvider({ children }: { children: ReactNode }) {
  const [toast, setToast] = useState<string | null>(null)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)

  const show = useCallback((message: string) => {
    clearTimeout(timer.current)
    setToast(message)
    timer.current = setTimeout(() => setToast(null), TOAST_MS)
  }, [])

  useEffect(() => () => clearTimeout(timer.current), [])

  const api = useMemo(() => ({ show }), [show])

  return (
    <ToastContext.Provider value={api}>
      {children}
      <div className="pointer-events-none fixed right-6 bottom-6 z-50">
        <div
          role="status"
          className={
            toast
              ? 'rounded-lg border border-border-strong bg-surface-2 px-4 py-3 text-sm text-text shadow-lg'
              : undefined
          }
        >
          {toast}
        </div>
      </div>
    </ToastContext.Provider>
  )
}

export function useToast(): ToastApi {
  const ctx = useContext(ToastContext)
  if (!ctx) throw new Error('useToast must be used inside a ToastProvider')
  return ctx
}
