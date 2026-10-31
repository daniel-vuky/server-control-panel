import { useEffect, useState } from 'react'
import { fetchMe } from '../api/me'

export function initials(email: string): string {
  const local = email.split('@')[0] ?? ''
  return local === '' ? '?' : local.slice(0, 2).toUpperCase()
}

export default function UserChip() {
  const [email, setEmail] = useState<string | null>(null)

  useEffect(() => {
    const ctrl = new AbortController()
    fetchMe(ctrl.signal).then((me) => {
      if (me && !ctrl.signal.aborted) setEmail(me.email)
    })
    return () => ctrl.abort()
  }, [])

  if (email === null) return null

  return (
    <div className="mt-auto flex items-center gap-2.5 border-t border-border px-2 pt-3">
      <div
        aria-hidden="true"
        className="flex size-7 flex-none items-center justify-center rounded-full bg-selected text-[11px] font-semibold text-text"
      >
        {initials(email)}
      </div>
      <div className="flex min-w-0 flex-col gap-0.5">
        <span className="truncate text-[13px]" title={email}>
          {email}
        </span>
        <span className="text-[11.5px] text-muted">Via Cloudflare Access</span>
      </div>
    </div>
  )
}
