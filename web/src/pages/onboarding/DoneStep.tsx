import { Check } from 'lucide-react'
import { Link } from 'react-router'
import { plural } from '../../lib/plural'

/** DoneStep is step 4 of the first-run setup (design 03): what was added and the way to the Dashboard. */
export default function DoneStep({ added }: { added: number }) {
  return (
    <section
      aria-labelledby="done-title"
      className="flex w-full max-w-[560px] flex-col items-center gap-[18px] rounded-[14px] border border-border bg-surface px-8 py-10 text-center"
    >
      <div
        aria-hidden="true"
        className="flex size-14 items-center justify-center rounded-full bg-success-bg text-success"
      >
        <Check size={28} strokeWidth={2.2} />
      </div>
      <h1 id="done-title" className="m-0 text-[22px] font-semibold">
        Added {plural(added, 'project', 'projects')}
      </h1>
      <p className="m-0 max-w-[400px] leading-relaxed text-muted">
        Status and resource usage update in real time. Schedule backups in each project&apos;s
        Database &amp; Backup tab.
      </p>
      <Link
        to="/"
        className="flex h-[42px] items-center rounded-lg bg-text px-[22px] font-semibold text-bg no-underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      >
        Go to Dashboard
      </Link>
    </section>
  )
}
