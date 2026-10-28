import { Link } from 'react-router'

export default function NotFoundPage() {
  return (
    <section className="flex flex-col gap-3">
      <h1 className="text-2xl font-semibold tracking-tight">Page not found</h1>
      <p className="text-muted">
        That page does not exist.{' '}
        <Link className="text-accent hover:underline" to="/">
          Back to Dashboard
        </Link>
      </p>
    </section>
  )
}
