import type { AppRoute } from '../app/routes'

const issueUrl = (n: number) => `https://github.com/daniel-vuky/server-control-panel/issues/${n}`

export default function PlaceholderPage({ route }: { route: AppRoute }) {
  return (
    <section className="flex flex-col gap-3">
      <h1 className="text-2xl font-semibold tracking-tight">{route.title}</h1>
      <p className="text-muted">
        Coming in{' '}
        {route.issues.map((n, i) => (
          <span key={n}>
            {i > 0 && ', '}
            <a
              className="font-mono text-accent hover:underline"
              href={issueUrl(n)}
              target="_blank"
              rel="noreferrer"
            >
              #{n}
            </a>
          </span>
        ))}
      </p>
    </section>
  )
}
