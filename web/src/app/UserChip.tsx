export function initials(email: string): string {
  const local = email.split('@')[0] ?? ''
  return local === '' ? '?' : local.slice(0, 2).toUpperCase()
}

/**
 * UserChip is the signed-in user at the foot of the nav: initials, email and
 * "Via Cloudflare Access". The shell reads /api/me once and passes the email;
 * null (not read yet, or the read failed) shows nothing.
 */
export default function UserChip({ email }: { email: string | null }) {
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
